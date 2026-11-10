// swift-tools-version: 6.2
import PackageDescription

let package = Package(
    name: "Hammock",
    platforms: [.macOS(.v26), .iOS(.v26)],
    products: [
        .library(name: "HammockKit", targets: ["HammockKit"]),
        .library(name: "HammockUpdates", targets: ["HammockUpdates"]),
        .executable(name: "HammockMac", targets: ["HammockMac"]),
        .executable(name: "HammockMenu", targets: ["HammockMenu"]),
        .executable(name: "hmk-probe", targets: ["HammockProbe"]),
    ],
    dependencies: [
        .package(url: "https://github.com/apple/swift-protobuf.git", from: "1.39.0"),
        .package(url: "https://github.com/facebook/zstd.git", exact: "1.5.7"),
        .package(url: "https://github.com/sparkle-project/Sparkle", from: "2.10.1"),
    ],
    targets: [
        // Built by `just ghostty && just apple-deps` from the pinned Ghostty commit.
        .binaryTarget(name: "GhosttyVt", path: "Frameworks/ghostty-vt.xcframework"),
        // The Mac app's proof to its daemon that a connection is its own
        // (helpers/common/appproof.h, shared with the daemon): a C shim for
        // DaemonConnection.
        .target(name: "HammockAppProof"),
        .target(
            name: "HammockKit",
            dependencies: [
                "GhosttyVt",
                .target(name: "HammockAppProof", condition: .when(platforms: [.macOS])),
                .product(name: "SwiftProtobuf", package: "swift-protobuf"),
                .product(name: "libzstd", package: "zstd"),
            ],
            swiftSettings: [.swiftLanguageMode(.v5)]
        ),
        // Updates through Sparkle (the Mac app only; the Xcode project links it too).
        .target(
            name: "HammockUpdates",
            dependencies: [.product(name: "Sparkle", package: "Sparkle")],
            swiftSettings: [.swiftLanguageMode(.v5)]
        ),
        .executableTarget(
            name: "HammockMac",
            dependencies: ["HammockKit", "HammockUpdates"],
            swiftSettings: [.swiftLanguageMode(.v5)]
        ),
        // The menu bar item (Hammock Menu.app, in Hammock.app's LoginItems).
        .executableTarget(
            name: "HammockMenu",
            dependencies: ["HammockKit"],
            swiftSettings: [.swiftLanguageMode(.v5)]
        ),
        .executableTarget(
            name: "HammockProbe",
            dependencies: ["HammockKit"],
            swiftSettings: [.swiftLanguageMode(.v5)]
        ),
        .testTarget(
            name: "HammockKitTests",
            dependencies: ["HammockKit"],
            swiftSettings: [.swiftLanguageMode(.v5)]
        ),
    ]
)
