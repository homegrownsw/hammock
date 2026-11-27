import AppKit
import Contacts
import CoreLocation
import HammockKit
import MapKit
import SwiftUI

/// A place automations can watch for this Mac arriving at or leaving
/// (place triggers and Only If; MacWatcher watches them). Kept in the
/// config file's `place` lines (`place = Office @ LAT,LON,METRES`): only
/// its name goes to the daemon, and from there to your phones and other
/// computers.
struct Place: Hashable, Identifiable, Sendable {
    var name: String
    var latitude: Double
    var longitude: Double
    /// How far from its centre still counts as there, in metres.
    var radius: Double = Place.defaultRadius

    var id: String { name }

    static let defaultRadius: Double = 150
    /// What CoreLocation watches well: much less, and a Mac's coarse
    /// location (Wi-Fi) can't tell.
    static let radiusChoices: [Double] = [100, 150, 250, 500, 1000, 2000]
    /// The longest name (the daemon's schedtrig.MaxPlaceName).
    static let maxName = 64

    var coordinate: CLLocationCoordinate2D { .init(latitude: latitude, longitude: longitude) }

    /// "Office @ 12.34560,-65.43210,150".
    var configValue: String {
        String(format: "%@ @ %.5f,%.5f,%.0f", name, latitude, longitude, radius)
    }

    init(name: String, latitude: Double, longitude: Double, radius: Double = Place.defaultRadius) {
        self.name = name
        self.latitude = latitude
        self.longitude = longitude
        self.radius = radius
    }

    /// Reads a `place` line's value; nil if it isn't one.
    init?(config: String) {
        guard let at = config.range(of: "@", options: .backwards) else { return nil }
        let name = config[..<at.lowerBound].trimmingCharacters(in: .whitespaces)
        let nums = config[at.upperBound...].split(separator: ",").map { Double($0.trimmingCharacters(in: .whitespaces)) }
        guard !name.isEmpty, nums.count >= 2, let lat = nums[0], let lon = nums[1],
              abs(lat) <= 90, abs(lon) <= 180 else { return nil }
        self.init(name: String(name.prefix(Self.maxName)), latitude: lat, longitude: lon,
                  radius: nums.count > 2 ? max(50, min(nums[2] ?? Self.defaultRadius, 50_000)) : Self.defaultRadius)
    }

    /// A radius in words: "150 m", "1 km".
    static func radiusText(_ m: Double) -> String {
        m >= 1000 ? (m.truncatingRemainder(dividingBy: 1000) == 0 ? "\(Int(m / 1000)) km" : String(format: "%.1f km", m / 1000)) : "\(Int(m)) m"
    }
}

/// The Places sheet: the places automations can watch, each on a map to
/// drop its pin on and size its circle. Opened from a place trigger's or
/// condition's token (New Place…, Edit Places…); done, picked hands back
/// the place chosen (a new one, or the one shown).
struct PlacesSheet: View {
    /// A new place to start on (New Place…), else the one to show.
    var startNew = false
    var selecting: String?
    var picked: ((String) -> Void)?
    @Environment(\.dismiss) private var dismiss
    @State private var places: [Place] = AppConfig.shared.places
    @State private var selected: String?
    @State private var draft = Place(name: "", latitude: 0, longitude: 0)
    @State private var isNew = false
    @State private var camera: MapCameraPosition = .automatic
    @State private var problem: String?
    @State private var finding = false

    var body: some View {
        HStack(spacing: 0) {
            list
                .frame(width: 180)
            Divider()
            editor
                .frame(minWidth: 460)
        }
        .frame(width: 680, height: 470)
        .onAppear {
            if startNew || places.isEmpty { newPlace() } else { select(selecting ?? places[0].name) }
        }
    }

    // MARK: The list

    private var list: some View {
        VStack(spacing: 0) {
            List(selection: Binding(get: { isNew ? nil : selected }, set: { if let n = $0 { select(n) } })) {
                ForEach(places) { p in
                    Label(p.name, systemImage: "mappin.circle").tag(p.name)
                }
                if isNew {
                    Label(draft.name.isEmpty ? "New Place" : draft.name, systemImage: "mappin.circle")
                        .foregroundStyle(.secondary)
                }
            }
            .listStyle(.sidebar)
            Divider()
            HStack(spacing: 4) {
                Button { newPlace() } label: { Image(systemName: "plus") }
                    .help("New Place")
                Button { deleteSelected() } label: { Image(systemName: "minus") }
                    .disabled(isNew || selected == nil)
                    .help("Delete Place")
                Spacer()
            }
            .buttonStyle(.borderless)
            .padding(8)
        }
    }

    // MARK: The editor

    private var editor: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 8) {
                TextField("Name", text: $draft.name, prompt: Text("Office"))
                    .font(.title3)
                    .textFieldStyle(.roundedBorder)
                Picker("Within", selection: $draft.radius) {
                    ForEach(radiusChoices, id: \.self) { r in Text("within \(Place.radiusText(r))").tag(r) }
                }
                .labelsHidden()
                .fixedSize()
            }
            MapReader { proxy in
                Map(position: $camera) {
                    if hasPin {
                        Marker(draft.name.isEmpty ? "Here" : draft.name, coordinate: draft.coordinate)
                            .tint(Color.themeAccent)
                        MapCircle(center: draft.coordinate, radius: draft.radius)
                            .foregroundStyle(Color.themeAccent.opacity(0.18))
                            .stroke(Color.themeAccent, lineWidth: 1.5)
                    }
                }
                .mapControls { MapZoomStepper() }
                .onTapGesture { pt in
                    if let c = proxy.convert(pt, from: .local) {
                        draft.latitude = c.latitude
                        draft.longitude = c.longitude
                    }
                }
                .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
                .help("Click to put the place there")
            }
            HStack(spacing: 8) {
                Button("Current Location") { Task { await useCurrentLocation() } }
                Menu("From My Card") {
                    Button("Home") { Task { await fromMyCard(home: true) } }
                    Button("Work") { Task { await fromMyCard(home: false) } }
                }
                .fixedSize()
                .help("Your home or work address, from your own card in Contacts")
                if finding { ProgressView().controlSize(.small) }
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button(picked == nil ? "Save" : "Use Place") { save() }
                    .keyboardShortcut(.defaultAction)
                    .buttonStyle(.borderedProminent)
                    .disabled(!canSave)
            }
            if let problem { Text(problem).font(.caption).foregroundStyle(.red) }
        }
        .padding(16)
    }

    private var radiusChoices: [Double] {
        Place.radiusChoices.contains(draft.radius) ? Place.radiusChoices : (Place.radiusChoices + [draft.radius]).sorted()
    }

    private var hasPin: Bool { draft.latitude != 0 || draft.longitude != 0 }

    private var trimmedName: String { draft.name.trimmingCharacters(in: .whitespaces) }

    private var canSave: Bool {
        let n = trimmedName
        guard !n.isEmpty, n.count <= Place.maxName, !n.contains("@"), hasPin else { return false }
        // Another place's name is taken.
        return !places.contains { $0.name.caseInsensitiveCompare(n) == .orderedSame && (isNew || $0.name != selected) }
    }

    // MARK: Actions

    private func select(_ name: String) {
        guard let p = places.first(where: { $0.name == name }) else { return }
        isNew = false
        selected = name
        draft = p
        problem = nil
        frame(p.coordinate, p.radius)
    }

    private func newPlace() {
        isNew = true
        selected = nil
        draft = Place(name: "", latitude: 0, longitude: 0)
        problem = nil
        camera = .automatic
        Task { await useCurrentLocation() }
    }

    private func frame(_ c: CLLocationCoordinate2D, _ radius: Double) {
        camera = .region(MKCoordinateRegion(center: c, latitudinalMeters: radius * 6, longitudinalMeters: radius * 6))
    }

    private func deleteSelected() {
        guard let name = selected else { return }
        places.removeAll { $0.name == name }
        AppConfig.shared.setPlaces(places)
        if let first = places.first { select(first.name) } else { newPlace() }
    }

    private func save() {
        var p = draft
        p.name = trimmedName
        if isNew {
            places.append(p)
        } else if let i = places.firstIndex(where: { $0.name == selected }) {
            places[i] = p
        }
        AppConfig.shared.setPlaces(places)
        MacWatcher.shared.requestLocation()
        picked?(p.name)
        dismiss()
    }

    private func useCurrentLocation() async {
        finding = true
        defer { finding = false }
        guard let loc = await OneLocation.current() else {
            problem = "Hammock can’t tell where this Mac is: it needs Location access (System Settings ▸ Privacy & Security ▸ Location Services)"
            return
        }
        problem = nil
        draft.latitude = loc.coordinate.latitude
        draft.longitude = loc.coordinate.longitude
        frame(loc.coordinate, draft.radius)
    }

    /// Home or Work: the address on your own card in Contacts, found on the
    /// map. Asks for Contacts access the first time.
    private func fromMyCard(home: Bool) async {
        finding = true
        defer { finding = false }
        let store = CNContactStore()
        guard (try? await store.requestAccess(for: .contacts)) == true else {
            problem = "Hammock may not read Contacts (System Settings ▸ Privacy & Security ▸ Contacts)"
            return
        }
        let keys = [CNContactPostalAddressesKey as CNKeyDescriptor]
        guard let me = try? store.unifiedMeContactWithKeys(toFetch: keys) else {
            problem = "There’s no card of your own in Contacts (Card ▸ Make This My Card)"
            return
        }
        let label = home ? CNLabelHome : CNLabelWork
        guard let address = me.postalAddresses.first(where: { $0.label == label })?.value else {
            problem = "Your card in Contacts has no \(home ? "home" : "work") address"
            return
        }
        let text = CNPostalAddressFormatter.string(from: address, style: .mailingAddress).replacingOccurrences(of: "\n", with: ", ")
        guard let request = MKGeocodingRequest(addressString: text),
              let item = try? await request.mapItems.first else {
            problem = "Couldn’t find that address on the map"
            return
        }
        let c = item.location.coordinate
        problem = nil
        if trimmedName.isEmpty { draft.name = home ? "Home" : "Work" }
        draft.latitude = c.latitude
        draft.longitude = c.longitude
        frame(c, draft.radius)
    }
}

/// Where this Mac is now, once (nil: no Location access, or it can't tell).
@MainActor
final class OneLocation: NSObject, CLLocationManagerDelegate {
    private let manager = CLLocationManager()
    private var done: CheckedContinuation<CLLocation?, Never>?
    private static var live: OneLocation?

    static func current() async -> CLLocation? {
        let one = OneLocation()
        live = one
        defer { live = nil }
        return await withCheckedContinuation { c in
            one.done = c
            one.manager.delegate = one
            one.manager.desiredAccuracy = kCLLocationAccuracyHundredMeters
            switch one.manager.authorizationStatus {
            case .notDetermined: one.manager.requestWhenInUseAuthorization()
            case .denied, .restricted: one.finish(nil)
            default: one.manager.requestLocation()
            }
        }
    }

    private func finish(_ l: CLLocation?) {
        done?.resume(returning: l)
        done = nil
    }

    nonisolated func locationManagerDidChangeAuthorization(_ manager: CLLocationManager) {
        Task { @MainActor in
            switch self.manager.authorizationStatus {
            case .notDetermined: break
            case .denied, .restricted: self.finish(nil)
            default: self.manager.requestLocation()
            }
        }
    }

    nonisolated func locationManager(_ manager: CLLocationManager, didUpdateLocations locations: [CLLocation]) {
        let l = locations.last
        Task { @MainActor in self.finish(l) }
    }

    nonisolated func locationManager(_ manager: CLLocationManager, didFailWithError error: any Error) {
        Task { @MainActor in self.finish(nil) }
    }
}
