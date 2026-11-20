import Foundation

/// Hammock's pages on the web.
public enum HammockLinks {
    /// The docs site (site/ in the repo): Help ▸ Hammock Documentation, and Settings on the iPhone.
    public static let docs = URL(string: "https://homegrownsw.com/hammock/docs/")!
    /// Troubleshooting: this computer's identity or certificate can't be read (Settings ▸ Devices).
    public static let damagedIdentity = URL(string: "https://homegrownsw.com/hammock/docs/troubleshooting/#remote-access-says-this-computers-identity-or-certificate-is-damaged")!
}
