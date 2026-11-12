import CoreGraphics
import RealityKit
import SwiftUI
#if os(macOS)
import AppKit
typealias IslandColor = NSColor
#else
import UIKit
typealias IslandColor = UIColor
#endif

/// Hammock Island's building blocks: materials, the meshes it makes itself,
/// and a little arithmetic.
enum Island {
    static func color(_ r: Double, _ g: Double, _ b: Double, _ a: Double = 1) -> IslandColor {
        IslandColor(red: r, green: g, blue: b, alpha: a)
    }

    /// Flat, matte colour with a little glow of its own, so shadows stay
    /// cartoon-soft without an environment light. The glow is the island's
    /// ambient light: IslandEffects dims and tints it as day turns to night.
    static func material(_ r: Double, _ g: Double, _ b: Double, opacity: Float = 1) -> PhysicallyBasedMaterial {
        var m = PhysicallyBasedMaterial()
        let c = color(r, g, b)
        m.baseColor = .init(tint: c)
        m.roughness = 0.9
        m.metallic = 0.0
        m.emissiveColor = .init(color: c)
        m.emissiveIntensity = 0.28
        if opacity < 1 { m.blending = .transparent(opacity: .init(floatLiteral: opacity)) }
        return m
    }

    /// Lit by nothing: the sun, the moon, stars, things that glow. Kept out
    /// of RealityKit's tone mapping, which turns bright colours muddy.
    static func unlit(_ r: Double, _ g: Double, _ b: Double, opacity: Float = 1) -> UnlitMaterial {
        glow(color(r, g, b), opacity: opacity)
    }

    static func glow(_ c: IslandColor, opacity: Float = 1) -> UnlitMaterial {
        var m = UnlitMaterial(applyPostProcessToneMap: false)
        m.color = .init(tint: c)
        if opacity < 1 { m.blending = .transparent(opacity: .init(floatLiteral: opacity)) }
        return m
    }

    /// A flat disc facing +z (towards the camera, on a billboard), `z` in
    /// front of its parent, at `at` across it.
    static func disc(radius: Float, _ material: UnlitMaterial, z: Float, at: SIMD2<Float> = .zero) -> ModelEntity {
        let d = ModelEntity(mesh: .generateCylinder(height: 0.002, radius: radius), materials: [material])
        d.orientation = simd_quatf(angle: .pi / 2, axis: [1, 0, 0])
        d.position = [at.x, at.y, z]
        return d
    }

    static func texture(_ image: CGImage?) -> TextureResource? {
        guard let image else { return nil }
        return try? TextureResource(image: image, options: .init(semantic: .color))
    }

    /// A square picture (the app's icon), seen from both sides.
    static func card(_ texture: TextureResource, size: Float) -> Entity {
        var m = UnlitMaterial(applyPostProcessToneMap: false)
        m.color = .init(tint: .white, texture: .init(texture))
        m.blending = .transparent(opacity: .init(floatLiteral: 1))
        let mesh = MeshResource.generatePlane(width: size, height: size)
        let card = Entity()
        card.addChild(ModelEntity(mesh: mesh, materials: [m]))
        let back = ModelEntity(mesh: mesh, materials: [m])
        back.orientation = simd_quatf(angle: .pi, axis: [0, 1, 0])
        card.addChild(back)
        return card
    }

    static let zMeshes: [MeshResource] = ["z", "Z"].map {
        MeshResource.generateText($0, extrusionDepth: 0.004, font: .systemFont(ofSize: 0.09, weight: .heavy))
    }

    static func text(_ string: String, size: CGFloat) -> MeshResource {
        MeshResource.generateText(string, extrusionDepth: 0.004, font: .systemFont(ofSize: size, weight: .heavy))
    }

    /// A drooping palm frond along +x, two-tone down its middle.
    static let leafMesh: MeshResource = surface(nu: 10, nv: 4, material: { _, j in j < 2 ? 0 : 1 }) { u, v in
        let length: Float = 0.75
        let width = 0.13 * pow(sin(.pi * min(1, u * 1.15)), 0.7) + 0.005
        let across = v * 2 - 1
        return [u * length, -0.42 * u * u - 0.03 * abs(across), across * width]
    }

    /// A flat ring of radius 1 (and a little), for foam and ripples.
    static let ringMesh: MeshResource = surface(nu: 64, nv: 1, material: { _, _ in 0 }) { u, v in
        let a = u * 2 * .pi
        let r = 1 + v * 0.07
        return [cos(a) * r, 0, sin(a) * r]
    }

    /// The hammock's cloth: sagging along x, curling up at its edges,
    /// gathered at its ends, striped lengthways.
    static func hammockMesh(half: Float, width: Float, sag: Float) -> MeshResource {
        surface(nu: 18, nv: 9, material: { _, j in UInt32(j % 3) }) { u, v in
            let s = u * 2 - 1
            let along = 1 - s * s
            let gather = 0.18 + 0.82 * sqrt(max(0, 1 - pow(abs(s), 3)))
            let across = v * 2 - 1
            return [s * half, -sag * along + 0.07 * across * across * along, across * width * gather]
        }
    }

    /// A two-sided grid surface through `f(u, v)` (both 0...1), with a
    /// material index per quad.
    static func surface(nu: Int, nv: Int, material: (Int, Int) -> UInt32, _ f: (Float, Float) -> SIMD3<Float>) -> MeshResource {
        var positions: [SIMD3<Float>] = []
        var normals: [SIMD3<Float>] = []
        let e: Float = 0.001
        for j in 0...nv {
            for i in 0...nu {
                let u = Float(i) / Float(nu), v = Float(j) / Float(nv)
                let du = f(min(1, u + e), v) - f(max(0, u - e), v)
                let dv = f(u, min(1, v + e)) - f(u, max(0, v - e))
                var n = cross(du, dv)
                n = length(n) > 0 ? normalize(n) : [0, 1, 0]
                positions.append(f(u, v))
                normals.append(n)
            }
        }
        let count = UInt32(positions.count)
        positions += positions
        normals += normals.map { -$0 }
        var indices: [UInt32] = []
        var faces: [UInt32] = []
        let row = UInt32(nu + 1)
        for j in 0..<nv {
            for i in 0..<nu {
                let a = UInt32(j) * row + UInt32(i), b = a + 1, c = a + row, d = c + 1
                let m = material(i, j)
                indices += [a, b, c, b, d, c]
                indices += [a + count, c + count, b + count, b + count, c + count, d + count]
                faces += [m, m, m, m]
            }
        }
        var descriptor = MeshDescriptor(name: "surface")
        descriptor.positions = MeshBuffer(positions)
        descriptor.normals = MeshBuffer(normals)
        descriptor.primitives = .triangles(indices)
        descriptor.materials = .perFace(faces)
        return try! MeshResource.generate(from: [descriptor])
    }

    // MARK: Arithmetic

    static func smoothstep(_ a: Float, _ b: Float, _ x: Float) -> Float {
        let t = min(1, max(0, (x - a) / (b - a)))
        return t * t * (3 - 2 * t)
    }

    static func mix(_ a: Float, _ b: Float, _ t: Float) -> Float { a + (b - a) * t }
    static func mix(_ a: SIMD3<Float>, _ b: SIMD3<Float>, _ t: Float) -> SIMD3<Float> { a + (b - a) * t }

    /// Somewhere on the island's (squashed) rim, `scale` times its radius out.
    static func rim(_ angle: Float, radius: Float, scale: Float = 1) -> SIMD3<Float> {
        [cos(angle) * radius * scale, 0, sin(angle) * radius * scale * 0.82]
    }

    /// How far out `p` is, as a multiple of the island's radius (1 is the shore).
    static func reach(_ p: SIMD3<Float>, radius: Float) -> Float {
        sqrt(p.x * p.x + (p.z / 0.82) * (p.z / 0.82)) / radius
    }
}
