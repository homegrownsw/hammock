// Hammock Key: the helper that holds this Mac's Hammock Key, an SSH key in
// the Secure Enclave, and decides when it signs.
//
// The key can't leave the Enclave. What the Enclave hands back is a handle
// that only works on this Mac, kept in a login-keychain item this helper
// made: macOS lets this helper (by its code signature) read it, and asks
// you before anything else may. The helper signs only with an approval it
// checks itself, so nothing that can talk to the daemon (or run this
// helper) gets a signature you didn't approve:
//   - Touch ID (or your password) here, with LocalAuthentication;
//   - a phone's approval: a signature by that phone's own Secure Enclave
//     key (Face ID or its passcode there) over this very request, from a
//     phone you let approve, with Touch ID here;
//   - a window you opened with one of those: a token (an HMAC with a key
//     only this helper knows) naming the pane and when it ends, which the
//     daemon keeps in memory and hands back.
//
// Only Hammock's daemon may ask it anything. Whatever runs this helper
// chooses its arguments, stdin and environment, so it takes none of those
// as the request: the daemon names a Mach service on the command line, the
// helper fetches the request from it, and takes it only if the kernel's
// audit token for that message says a running hmkd signed by this helper's
// own team, with the hardened runtime, sent it (rendezvous.h says why that
// holds where the parent's pid, or a socket's, doesn't). An ad-hoc
// development build has no team: there the sender must be the hmkd beside
// this helper. So an agent in a pane can't run it itself to skip the
// daemon's rules (an agent's window only its own, the audit log).
//
// And the prompt says what will happen in the helper's own words: what's
// signed, from the data; creating or deleting the key, or the device it
// would let approve, by that device's key; that allowing opens a window,
// and for how long the helper lets one last. The daemon's words are only
// where a request is from (the Group, tab, agent, command), labelled so.
// The limit: a build of this helper signed with the same identity, from
// changed code, reads the keychain item too (β's has an identifier of its
// own, so it can't silently).
//
// One request per run, from the daemon (keyHelperProtocol in hammockkey.go):
//   {"v": 1, "command", "beta", "input": {...}}, where command and input are
//   status          {}
//   create          {"approvers": [approver]}
//   delete          {}
//   add-approver    {"approver"}
//   remove-approver {"id"}
//   sign            {"data", "pane", "agent", "window", "context", "prompt": {...},
//                    "approval": {...}, "token"}
// ("prompt": where it's from, for the panel: keyPrompt in hammockkey.go.)
// Out on stdout: exit 0 with JSON, or 1 with a message on stderr; 2 when
// you said no (or the daemon withdrew the prompt: SIGTERM). "beta" keeps
// Hammock β's key apart from the real one.
//
//   hmk-key --daemon <service>
//   hmk-key preview [--snapshot <prefix>] < {"command", ...} or {"headline", "window", "agent", "prompt"}
// (preview shows a prompt, or renders it, signing nothing: anyone may run it.)
import CryptoKit
import Foundation
import LocalAuthentication
import Security

struct Approver: Codable {
    var id: String
    var name: String
    var key: String // base64 x9.63 P-256 public key (the phone's Enclave key)
}

struct Stored: Codable {
    var key: String // base64 Enclave key handle
    var hmac: String // base64 32 bytes: window tokens
    var approvers: [Approver]
    var created: Int64
}

struct Fail: Error { var message: String; var code: Int32 = 1 }

let args = Array(CommandLine.arguments.dropFirst())
/// The request's version, as the daemon's keyHelperProtocol.
let protocolVersion = 1
/// How long a window lasts: the daemon's hammockKeyWindow, and what the
/// prompt says.
let keyWindowMs: Int64 = 10 * 60 * 1000
/// An agent's: the daemon's hammockKeyAgentWindow. Shorter, and only if you
/// choose it (the prompt's box, the phone's button): a script's few
/// commits, a burst of ssh to a remote machine.
let agentKeyWindowMs: Int64 = 5 * 60 * 1000
/// β's key, not the real one: from the daemon's request.
var beta = false
var service: String { beta ? "com.homegrownsw.hammock.key.beta" : "com.homegrownsw.hammock.key" }
var label: String { beta ? "Hammock Key (β)" : "Hammock Key" }

/// JSON on stdin: only for preview, which signs nothing.
func stdinJSON() -> [String: Any] {
    let data = FileHandle.standardInput.readDataToEndOfFile()
    guard !data.isEmpty, let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return [:] }
    return obj
}

func output(_ obj: [String: Any]) {
    let data = (try? JSONSerialization.data(withJSONObject: obj, options: [.sortedKeys])) ?? Data("{}".utf8)
    FileHandle.standardOutput.write(data)
    FileHandle.standardOutput.write(Data("\n".utf8))
}

// MARK: - Keychain

func load() throws -> Stored? {
    var out: CFTypeRef?
    let st = SecItemCopyMatching([
        kSecClass: kSecClassGenericPassword, kSecAttrService: service, kSecAttrAccount: "key",
        kSecReturnData: true,
    ] as CFDictionary, &out)
    if st == errSecItemNotFound { return nil }
    guard st == errSecSuccess, let data = out as? Data else {
        throw Fail(message: "can't read the Hammock Key from the keychain (\(st))")
    }
    return try JSONDecoder().decode(Stored.self, from: data)
}

func save(_ s: Stored) throws {
    let data = try JSONEncoder().encode(s)
    let q: [CFString: Any] = [kSecClass: kSecClassGenericPassword, kSecAttrService: service, kSecAttrAccount: "key"]
    let st = SecItemUpdate(q as CFDictionary, [kSecValueData: data] as CFDictionary)
    if st == errSecItemNotFound {
        var add = q
        add[kSecValueData] = data
        add[kSecAttrLabel] = label
        add[kSecAttrDescription] = "Hammock's SSH key: its Secure Enclave handle"
        let st2 = SecItemAdd(add as CFDictionary, nil)
        guard st2 == errSecSuccess else { throw Fail(message: "can't save the Hammock Key in the keychain (\(st2))") }
    } else if st != errSecSuccess {
        throw Fail(message: "can't save the Hammock Key in the keychain (\(st))")
    }
}

func remove() {
    SecItemDelete([kSecClass: kSecClassGenericPassword, kSecAttrService: service, kSecAttrAccount: "key"] as CFDictionary)
}

func enclaveKey(_ s: Stored) throws -> SecureEnclave.P256.Signing.PrivateKey {
    guard let blob = Data(base64Encoded: s.key) else { throw Fail(message: "the Hammock Key is damaged") }
    do {
        return try SecureEnclave.P256.Signing.PrivateKey(dataRepresentation: blob)
    } catch {
        throw Fail(message: "this Mac's Secure Enclave doesn't know the Hammock Key (\(error.localizedDescription))")
    }
}

// MARK: - Approval here

/// The context being evaluated, so SIGTERM (the daemon withdrawing the
/// prompt: you approved on your phone) can take the dialog down.
var current: LAContext?

/// Asks you here: in Hammock's own panel (panel.swift) when there's Touch ID,
/// else (or when you'd rather type your password) the system sheet. Only
/// your "Don't Allow" is a no (exit 2): anything else (the screen locked,
/// nobody to ask) leaves the answer to your phone. Returns whether you
/// ticked the panel's box for an agent's window (the system sheet has none:
/// shown on its own, it opens none).
@discardableResult
func touchID(_ reason: String, prompt: KeyPrompt? = nil) throws -> Bool {
    let choice = WindowChoice()
    switch askInPanel(prompt ?? KeyPrompt(headline: reason), reason: reason, choice: choice) {
    case .allowed: return choice.on
    case .declined: throw Fail(message: "not allowed", code: 2)
    case .password, .unavailable: break
    }
    let ctx = LAContext()
    ctx.localizedCancelTitle = "Don't Allow"
    var err: NSError?
    guard ctx.canEvaluatePolicy(.deviceOwnerAuthentication, error: &err) else {
        throw Fail(message: "can't ask you here: \(err?.localizedDescription ?? "no way to authenticate")")
    }
    current = ctx
    let done = DispatchSemaphore(value: 0)
    var ok = false
    var failure: Error?
    ctx.evaluatePolicy(.deviceOwnerAuthentication, localizedReason: reason) { success, error in
        ok = success
        failure = error
        done.signal()
    }
    done.wait()
    current = nil
    if !ok {
        let said = (failure as? LAError)?.code == .userCancel
        throw Fail(message: failure?.localizedDescription ?? "not allowed", code: said ? 2 : 1)
    }
    return choice.on // ticked before "Use Password…"; never, if the panel wasn't shown
}

// MARK: - Phone approvals

/// What a phone signs to approve a request (the phone builds the same
/// bytes: HammockKeyApproval.message in HammockKit), naming the key it's for: an
/// approval for one Mac's key (or β's) is no use with another's.
func approvalMessage(key: String, request: String, dataHash: String, scope: String, pane: String, expires: Int64) -> Data {
    Data("hmk-key-approval-v2\n\(key)\n\(request)\n\(dataHash)\n\(scope)\n\(pane)\n\(expires)".utf8)
}

/// The key's id: the hex SHA-256 of its public key (x9.63).
func keyID(_ publicKey: Data) -> String { hex(SHA256.hash(data: publicKey)) }

func hex<D: Sequence>(_ d: D) -> String where D.Element == UInt8 { d.map { String(format: "%02x", $0) }.joined() }

func checkApproval(_ a: [String: Any], data: Data, pane: String, stored: Stored, key: String) throws -> String {
    guard let request = a["request"] as? String, let approver = a["approver"] as? String,
          let scope = a["scope"] as? String, let sigB64 = a["signature"] as? String,
          let sigData = Data(base64Encoded: sigB64)
    else { throw Fail(message: "a malformed approval") }
    let expires = (a["expires"] as? NSNumber)?.int64Value ?? 0
    let now = Int64(Date().timeIntervalSince1970 * 1000)
    guard expires > now, expires - now <= 5 * 60 * 1000 else { throw Fail(message: "the approval has expired") }
    guard scope == "once" || scope == "window" else { throw Fail(message: "an approval of an unknown kind") }
    guard let who = stored.approvers.first(where: { $0.id == approver }),
          let keyData = Data(base64Encoded: who.key),
          let phone = try? P256.Signing.PublicKey(x963Representation: keyData)
    else { throw Fail(message: "that device can't approve the Hammock Key") }
    let msg = approvalMessage(key: key, request: request, dataHash: hex(SHA256.hash(data: data)), scope: scope, pane: pane, expires: expires)
    guard let sig = try? P256.Signing.ECDSASignature(derRepresentation: sigData), phone.isValidSignature(sig, for: msg) else {
        throw Fail(message: "the approval's signature doesn't check out")
    }
    return scope
}

// MARK: - What's signed, in the helper's own words

/// What signing data does: an SSH login (RFC 4252: session id, then
/// SSH_MSG_USERAUTH_REQUEST and the user) or an ssh-keygen -Y signature
/// ("SSHSIG", then the namespace).
func describe(_ data: Data) -> String {
    func string(_ b: Data, at i: Int) -> (Data, Int)? {
        guard b.count >= i + 4 else { return nil }
        let n = b[b.startIndex + i ..< b.startIndex + i + 4].reduce(0) { $0 << 8 | Int($1) }
        guard b.count >= i + 4 + n else { return nil }
        return (b[b.startIndex + i + 4 ..< b.startIndex + i + 4 + n], i + 4 + n)
    }
    let printable = { (d: Data) in String(decoding: d, as: UTF8.self).filter { !$0.isNewline && $0.isASCII }.prefix(40) }
    if data.starts(with: Data("SSHSIG".utf8)), let (ns, _) = string(data, at: 6) {
        let n = printable(ns)
        return n == "git" ? "sign for git (a commit or tag)" : "sign for \(n)"
    }
    if let (_, i) = string(data, at: 0), data.count > i, data[data.startIndex + i] == 50, let (user, _) = string(data, at: i + 1) {
        return "log in as \(printable(user)) with SSH"
    }
    return "sign with your Hammock Key"
}

// MARK: - Who's asking

func teamID(_ code: SecStaticCode) -> String? {
    var info: CFDictionary?
    guard SecCodeCopySigningInformation(code, SecCSFlags(rawValue: kSecCSSigningInformation), &info) == errSecSuccess,
          let d = info as? [String: Any] else { return nil }
    return d[kSecCodeInfoTeamIdentifier as String] as? String
}

func ownCode() -> SecStaticCode? {
    var me: SecCode?
    var meStatic: SecStaticCode?
    guard SecCodeCopySelf([], &me) == errSecSuccess, let me, SecCodeCopyStaticCode(me, [], &meStatic) == errSecSuccess else { return nil }
    return meStatic
}

/// This helper is a development build, signed ad hoc (no team).
func adHoc() -> Bool { ownCode().map { teamID($0) == nil } ?? false }

let notFromHammock = Fail(message: "only Hammock's daemon can use the Hammock Key (hmk key, Settings ▸ SSH, or ssh in a Hammock pane)")

/// The request, from the daemon's service (rendezvous.h), if Hammock's
/// daemon sent it.
func fetchRequest(_ name: String) throws -> [String: Any] {
    var data: UnsafeMutableRawPointer?
    var len = 0
    var token = audit_token_t()
    let kr = hmk_key_fetch(name, 10_000, &data, &len, &token)
    guard kr == KERN_SUCCESS, let data else { throw notFromHammock }
    let bytes = Data(bytes: data, count: len)
    vm_deallocate(mach_task_self_, vm_address_t(UInt(bitPattern: data)), vm_size_t(len))
    guard sentByHammock(token) else { throw notFromHammock }
    guard let req = try? JSONSerialization.jsonObject(with: bytes) as? [String: Any] else {
        throw Fail(message: "the daemon's request is malformed")
    }
    guard (req["v"] as? NSNumber)?.intValue == protocolVersion else {
        throw Fail(message: "this Hammock Key helper is from another build of Hammock than its daemon: reinstall Hammock")
    }
    return req
}

/// The message's sender, by the audit token the kernel gave it as it was
/// sent, is Hammock's daemon: hmkd, signed by this helper's team, with the
/// hardened runtime (nothing injected into it, no debugger attached).
/// Ad hoc, the hmkd beside this helper.
func sentByHammock(_ token: audit_token_t) -> Bool {
    var t = token
    let audit = Data(bytes: &t, count: MemoryLayout<audit_token_t>.size)
    var guest: SecCode?
    // Fails if the sender has exec'd since it sent it: the token's pid
    // version is that of the program that sent it.
    guard SecCodeCopyGuestWithAttributes(nil, [kSecGuestAttributeAudit: audit] as CFDictionary, [], &guest) == errSecSuccess,
          let guest, let me = ownCode() else { return false }
    guard let team = teamID(me) else { return besideThisHelper(guest) }
    var req: SecRequirement?
    let text = "anchor apple generic and certificate leaf[subject.OU] = \"\(team)\" and identifier \"hmkd\""
    guard SecRequirementCreateWithString(text as CFString, [], &req) == errSecSuccess, let req,
          SecCodeCheckValidity(guest, [], req) == errSecSuccess else { return false }
    var st: SecStaticCode?
    var info: CFDictionary?
    guard SecCodeCopyStaticCode(guest, [], &st) == errSecSuccess, let st,
          SecCodeCopySigningInformation(st, SecCSFlags(rawValue: kSecCSSigningInformation | kSecCSDynamicInformation), &info) == errSecSuccess,
          let d = info as? [String: Any], let flags = (d[kSecCodeInfoFlags as String] as? NSNumber)?.uint32Value
    else { return false }
    return flags & SecCodeSignatureFlags.runtime.rawValue != 0
}

/// A development build's check, with no team to check against: the sender
/// runs, unchanged, the hmkd of this helper's own build (the app's
/// Contents/MacOS/hmkd for its Contents/Helpers/Hammock Key.app, or bin/hmkd
/// for bin/Hammock Key.app).
func besideThisHelper(_ guest: SecCode) -> Bool {
    var st: SecStaticCode?
    var url: CFURL?
    guard SecCodeCheckValidity(guest, [], nil) == errSecSuccess,
          SecCodeCopyStaticCode(guest, [], &st) == errSecSuccess, let st,
          SecCodeCopyPath(st, [], &url) == errSecSuccess, let url else { return false }
    let here = Bundle.main.bundleURL.deletingLastPathComponent()
    let hmkd = here.lastPathComponent == "Helpers"
        ? here.deletingLastPathComponent().appendingPathComponent("MacOS/hmkd")
        : here.appendingPathComponent("hmkd")
    return (url as URL).resolvingSymlinksInPath().path == hmkd.resolvingSymlinksInPath().path
}

// MARK: - The prompts for changing the key, in the helper's words

/// A short fingerprint of a device's approval key (base64 x9.63): the
/// first 64 bits of its SHA-256, as four groups of hex.
func fingerprint(_ key: String) -> String {
    let id = keyID(Data(base64Encoded: key) ?? Data())
    return stride(from: 0, to: 16, by: 4).map { i in
        String(id[id.index(id.startIndex, offsetBy: i) ..< id.index(id.startIndex, offsetBy: i + 4)])
    }.joined(separator: " ")
}

/// A device's name as the daemon has it (the name the phone gave when it
/// paired), on one line and not too long.
func deviceName(_ name: String) -> String {
    let s = String(name.filter { !$0.isNewline && !($0.unicodeScalars.first?.properties.generalCategory == .control) }.prefix(60))
    return s.isEmpty ? "A device" : s
}

/// What creating or deleting the key, or letting a device approve it,
/// does: the panel's prompt and the system sheet's reason. Fixed words:
/// nothing the daemon sends is in a headline, only the devices' names,
/// each beside its key's fingerprint.
func changePrompt(_ command: String, approvers: [Approver] = []) -> (KeyPrompt, String) {
    var p = KeyPrompt(headline: "")
    p.devices = approvers.map { KeyPrompt.Device(name: deviceName($0.name), fingerprint: fingerprint($0.key)) }
    let keys = approvers.map { fingerprint($0.key) }.joined(separator: ", ")
    switch command {
    case "create":
        if approvers.isEmpty {
            p.headline = "Create your Hammock Key"
            return (p, "create your Hammock Key")
        }
        let them = approvers.count == 1 ? "a device" : "\(approvers.count) devices"
        p.headline = "Create your Hammock Key, and let \(them) approve it without Touch ID"
        p.notes = [(approvers.count == 1 ? "This device" : "Each of these devices")
            + " will be able to approve signatures with your Hammock Key without Touch ID on this Mac, until you remove it in Settings ▸ SSH."]
        return (p, "create your Hammock Key, and let \(them) (key \(keys)) approve signatures with it without Touch ID on this Mac")
    case "delete":
        p.headline = "Delete your Hammock Key"
        p.notes = ["Servers, GitHub and anything else that has its public key won't accept it any more. This can't be undone: a new key would be a different one."]
        return (p, "delete your Hammock Key")
    default: // add-approver
        p.headline = "Let a device approve signatures without Touch ID"
        p.notes = ["This device will be able to approve signatures with your Hammock Key without Touch ID on this Mac, until you remove it in Settings ▸ SSH. Allow only if you've just asked for this, for your own phone."]
        return (p, "let a device (key \(keys)) approve signatures with your Hammock Key without Touch ID on this Mac")
    }
}

// MARK: - Window tokens

/// A window's token: the pane and when it ends, and "agent" for an agent's,
/// so neither kind's token serves the other.
func token(pane: String, agent: Bool, until: Int64, stored: Stored) throws -> String {
    guard let k = Data(base64Encoded: stored.hmac) else { throw Fail(message: "the Hammock Key is damaged") }
    let payload = Data((agent ? "\(pane)\n\(until)\nagent" : "\(pane)\n\(until)").utf8)
    let mac = HMAC<SHA256>.authenticationCode(for: payload, using: SymmetricKey(data: k))
    return payload.base64EncodedString() + "." + Data(mac).base64EncodedString()
}

func checkToken(_ t: String, pane: String, agent: Bool, stored: Stored) -> Bool {
    let parts = t.split(separator: ".")
    guard parts.count == 2, let payload = Data(base64Encoded: String(parts[0])), let mac = Data(base64Encoded: String(parts[1])),
          let k = Data(base64Encoded: stored.hmac),
          HMAC<SHA256>.isValidAuthenticationCode(mac, authenticating: payload, using: SymmetricKey(data: k)),
          let text = String(data: payload, encoding: .utf8)
    else { return false }
    let fields = text.split(separator: "\n", omittingEmptySubsequences: false)
    let isAgents = fields.count == 3 && fields[2] == "agent"
    guard fields.count == 2 || isAgents, isAgents == agent, String(fields[0]) == pane, let until = Int64(fields[1]) else {
        return false
    }
    return until > Int64(Date().timeIntervalSince1970 * 1000)
}

// MARK: - Commands

func status(_ s: Stored?) throws -> [String: Any] {
    guard let s else { return ["exists": false, "enclave": SecureEnclave.isAvailable] }
    let key = try enclaveKey(s)
    return [
        "exists": true, "enclave": true, "created": s.created,
        "public_key": key.publicKey.x963Representation.base64EncodedString(),
        "key_id": keyID(key.publicKey.x963Representation),
        "approvers": s.approvers.map { ["id": $0.id, "name": $0.name, "key": $0.key] },
    ]
}

func parseApprover(_ v: Any?) throws -> Approver {
    guard let o = v as? [String: Any], let id = o["id"] as? String, let name = o["name"] as? String,
          let key = o["key"] as? String, let kd = Data(base64Encoded: key),
          (try? P256.Signing.PublicKey(x963Representation: kd)) != nil
    else { throw Fail(message: "a malformed approver") }
    return Approver(id: id, name: name, key: key)
}

func run() throws {
    if args.first == "preview" { return preview(args) } // signs nothing
    guard args.count == 2, args[0] == "--daemon" else { throw notFromHammock }
    let request = try fetchRequest(args[1])
    beta = request["beta"] as? Bool ?? true
    let command = request["command"] as? String ?? ""
    let req = request["input"] as? [String: Any] ?? [:]
    switch command {
    case "status":
        output(try status(try load()))

    case "create":
        guard SecureEnclave.isAvailable else { throw Fail(message: "this Mac has no Secure Enclave") }
        if try load() != nil { throw Fail(message: "this Mac already has a Hammock Key") }
        let approvers = try (req["approvers"] as? [Any] ?? []).map(parseApprover)
        let (prompt, reason) = changePrompt("create", approvers: approvers)
        try touchID(reason, prompt: prompt)
        // Usable while the screen's locked (you're away: your phone
        // approves), never off this Mac.
        var acErr: Unmanaged<CFError>?
        guard let access = SecAccessControlCreateWithFlags(nil, kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly, .privateKeyUsage, &acErr) else {
            throw Fail(message: "can't make the Hammock Key: \(acErr!.takeRetainedValue())")
        }
        let key = try SecureEnclave.P256.Signing.PrivateKey(accessControl: access)
        var mac = Data(count: 32)
        _ = mac.withUnsafeMutableBytes { SecRandomCopyBytes(kSecRandomDefault, 32, $0.baseAddress!) }
        let s = Stored(key: key.dataRepresentation.base64EncodedString(), hmac: mac.base64EncodedString(),
                       approvers: approvers, created: Int64(Date().timeIntervalSince1970 * 1000))
        try save(s)
        output(try status(s))

    case "delete":
        guard try load() != nil else { output(["exists": false]); return }
        let (prompt, reason) = changePrompt("delete")
        try touchID(reason, prompt: prompt)
        remove()
        output(["exists": false])

    case "add-approver":
        guard var s = try load() else { throw Fail(message: "there's no Hammock Key") }
        let a = try parseApprover(req["approver"])
        let (prompt, reason) = changePrompt("add-approver", approvers: [a])
        try touchID(reason, prompt: prompt)
        s.approvers.removeAll { $0.id == a.id }
        s.approvers.append(a)
        try save(s)
        output(try status(s))

    case "remove-approver":
        // Taking a device's say away needs no approval: it only ever
        // narrows who can approve, only Hammock's daemon can ask (for you,
        // in Settings, or as you unpair the phone, which shouldn't leave it
        // able to approve until you say so here), and adding it back asks.
        guard var s = try load() else { throw Fail(message: "there's no Hammock Key") }
        let id = req["id"] as? String ?? ""
        s.approvers.removeAll { $0.id == id }
        try save(s)
        output(try status(s))

    case "sign":
        guard let s = try load() else { throw Fail(message: "there's no Hammock Key") }
        guard let dataB64 = req["data"] as? String, let data = Data(base64Encoded: dataB64) else {
            throw Fail(message: "nothing to sign")
        }
        let pane = req["pane"] as? String ?? ""
        // Allowing here opens a window too, for as long as this helper says:
        // for a program of yours in a pane, always; for an agent, only if you
        // tick the panel's box. A phone's approval says for itself whether
        // it opens one. An agent's window is its own: its token says so.
        let askWindow = (req["window"] as? Bool ?? false) && !pane.isEmpty
        let agent = req["agent"] as? Bool ?? false
        let key = try enclaveKey(s)
        var window = false
        if let t = req["token"] as? String {
            guard !pane.isEmpty, checkToken(t, pane: pane, agent: agent, stored: s) else {
                throw Fail(message: "that approval has run out", code: 2)
            }
        } else if let a = req["approval"] as? [String: Any] {
            window = try checkApproval(a, data: data, pane: pane, stored: s, key: keyID(key.publicKey.x963Representation)) == "window"
        } else {
            // In the helper's words what's signed; the daemon's (where, who,
            // which program) after it.
            var reason = describe(data)
            if let context = req["context"] as? String, !context.isEmpty {
                reason += " (" + String(context.prefix(160)) + ")"
            }
            // (An agent's window is the panel's box: the system sheet on its
            // own, which has none, never opens one.)
            if askWindow, !agent {
                reason += ", and let programs in that pane sign for the next \(keyWindowMs / 60000) minutes without asking"
            }
            let ticked = try touchID(reason, prompt: KeyPrompt(headline: describe(data), from: req["prompt"] as? [String: Any],
                                                               window: askWindow, agent: agent))
            window = askWindow && (!agent || ticked)
        }
        let sig = try key.signature(for: data)
        var out: [String: Any] = ["signature": sig.rawRepresentation.base64EncodedString()]
        if window, !pane.isEmpty {
            let until = Int64(Date().timeIntervalSince1970 * 1000) + (agent ? agentKeyWindowMs : keyWindowMs)
            out["token"] = try token(pane: pane, agent: agent, until: until, stored: s)
            out["until"] = until
        }
        output(out)

    default:
        throw Fail(message: "the daemon asked for something this helper doesn't do (\(command))")
    }
}

signal(SIGTERM, SIG_IGN)
let term = DispatchSource.makeSignalSource(signal: SIGTERM, queue: .global())
term.setEventHandler {
    current?.invalidate()
    exit(2)
}
term.resume()

do {
    try run()
} catch let f as Fail {
    FileHandle.standardError.write(Data((f.message + "\n").utf8))
    exit(f.code)
} catch {
    FileHandle.standardError.write(Data((error.localizedDescription + "\n").utf8))
    exit(1)
}
