import RealityKit
import SwiftUI

/// Hammock Island's little effects: things that drift, spin, grow and fade
/// (z's, ripples, the icon rising from a bottle), bursts of particles
/// (splashes, sparkles, fireworks), rain, and the ambient light every lit
/// material shares, dimmed and tinted as the day goes round.
@MainActor
final class IslandEffects {
    private struct Floater {
        let entity: Entity
        var age: Float = 0
        let life: Float
        var velocity: SIMD3<Float>
        let gravity: Float
        let spin: Float
        let from: SIMD3<Float>
        let to: SIMD3<Float>
        let fadeAt: Float
        let opacity: Float
        let then: (() -> Void)?
    }
    private var floaters: [Floater] = []
    private var expiring: [(Entity, Float)] = []
    private var stopping: [(Entity, Float)] = []
    private var t: Float = 0

    // MARK: Floaters

    /// Adds `entity` to `parent` at `position`, moves, spins and scales it
    /// over `life` seconds, fades it out from `fadeAt` (a fraction of its
    /// life), then removes it and calls `then`. Fading only reaches unlit
    /// materials; anything lit just shrinks with `to`.
    func float(_ entity: Entity, in parent: Entity, at position: SIMD3<Float>, life: Float,
               velocity: SIMD3<Float> = .zero, gravity: Float = 0, spin: Float = 0,
               from: SIMD3<Float> = .one, to: SIMD3<Float> = .one, fadeAt: Float = 0.5, opacity: Float = 1,
               then: (() -> Void)? = nil) {
        entity.position = position
        entity.scale = from
        if entity.parent !== parent { parent.addChild(entity) }
        floaters.append(Floater(entity: entity, life: life, velocity: velocity, gravity: gravity, spin: spin,
                                from: from, to: to, fadeAt: fadeAt, opacity: opacity, then: then))
        Self.setOpacity(entity, opacity)
    }

    /// A ring spreading out over the water from `position`.
    func ripple(in parent: Entity, at position: SIMD3<Float>, size: Float = 0.35, life: Float = 1.3,
                color: IslandColor = .white, opacity: Float = 0.8) {
        var m = Island.glow(color)
        m.blending = .transparent(opacity: .init(floatLiteral: opacity))
        let ring = ModelEntity(mesh: Island.ringMesh, materials: [m])
        float(ring, in: parent, at: position, life: life, from: SIMD3(repeating: size * 0.12), to: SIMD3(repeating: size),
              fadeAt: 0, opacity: opacity)
    }

    func expire(_ entity: Entity, after seconds: Float) { expiring.append((entity, t + seconds)) }

    // MARK: Particles

    /// A one-off burst of particles at `position`.
    func burst(in parent: Entity, at position: SIMD3<Float>, colors: [IslandColor], count: Int, speed: Float,
               size: Float, life: Float, gravity: Float, spread: Float = .pi / 3, direction: SIMD3<Float> = [0, 1, 0]) {
        let e = Entity()
        e.position = position
        var p = ParticleEmitterComponent()
        p.emitterShape = .sphere
        p.emitterShapeSize = SIMD3(repeating: size)
        p.emissionDirection = direction
        p.speed = speed
        p.speedVariation = speed * 0.4
        p.mainEmitter.birthRate = 0
        p.mainEmitter.lifeSpan = Double(life)
        p.mainEmitter.lifeSpanVariation = Double(life * 0.3)
        p.mainEmitter.size = size
        p.mainEmitter.sizeVariation = size * 0.4
        p.mainEmitter.spreadingAngle = spread
        p.mainEmitter.acceleration = [0, -gravity, 0]
        p.mainEmitter.opacityCurve = .linearFadeOut
        p.mainEmitter.color = colors.count > 1
            ? .constant(.random(a: colors[0], b: colors[1]))
            : .constant(.single(colors.first ?? .white))
        p.burstCount = count
        p.burstCountVariation = count / 4
        p.isEmitting = false
        e.components.set(p)
        parent.addChild(e)
        if var c = e.components[ParticleEmitterComponent.self] {
            c.burst()
            e.components.set(c)
        }
        expire(e, after: life * 1.6 + 0.3)
    }

    /// Water splashing up, as something lands in the sea.
    func splash(in parent: Entity, at position: SIMD3<Float>, big: Bool = false) {
        burst(in: parent, at: position, colors: [Island.color(1, 1, 1), Island.color(0.6, 0.9, 1)],
              count: big ? 70 : 25, speed: big ? 1.6 : 1.0, size: big ? 0.035 : 0.022, life: 0.7, gravity: 5, spread: 0.6)
        ripple(in: parent, at: position, size: big ? 0.7 : 0.35)
    }

    /// Rain falling from `parent` (a cloud) for `seconds`.
    func rain(from parent: Entity, at position: SIMD3<Float>, seconds: Float) {
        let e = Entity()
        e.position = position
        var p = ParticleEmitterComponent()
        p.emitterShape = .box
        p.emitterShapeSize = [4, 0.2, 1.5]
        p.emissionDirection = [0, -1, 0]
        p.speed = 6
        p.mainEmitter.birthRate = 160
        p.mainEmitter.lifeSpan = 1.4
        p.mainEmitter.size = 0.07
        p.mainEmitter.spreadingAngle = 0.05
        p.mainEmitter.color = .constant(.single(Island.color(0.75, 0.85, 1)))
        p.mainEmitter.stretchFactor = 4
        e.components.set(p)
        parent.addChild(e)
        stopping.append((e, t + seconds))
        expire(e, after: seconds + 1.6)
    }

    // MARK: Ambient light

    private var tinted: [ObjectIdentifier: (ModelEntity, [PhysicallyBasedMaterial])] = [:]
    private var ambient: Float = 0.28
    private var ambientColor: SIMD3<Float> = [1, 1, 1]

    /// Puts `root` and everything in it under the island's ambient light.
    func tint(_ root: Entity) {
        var found: [ModelEntity] = []
        func walk(_ e: Entity) {
            if let m = e as? ModelEntity { found.append(m) }
            e.children.forEach(walk)
        }
        walk(root)
        for m in found {
            guard let materials = m.model?.materials.compactMap({ $0 as? PhysicallyBasedMaterial }),
                  materials.count == m.model?.materials.count, !materials.isEmpty else { continue }
            tinted[ObjectIdentifier(m)] = (m, materials)
            apply(m, materials)
        }
    }

    func setAmbient(_ intensity: Float, color: SIMD3<Float>) {
        guard abs(intensity - ambient) > 0.004 || simd_distance(color, ambientColor) > 0.01 else { return }
        ambient = intensity
        ambientColor = color
        for (key, (m, materials)) in tinted {
            guard m.parent != nil else { tinted[key] = nil; continue }
            apply(m, materials)
        }
    }

    private func apply(_ m: ModelEntity, _ materials: [PhysicallyBasedMaterial]) {
        m.model?.materials = materials.map { original in
            var c = original
            let base = Self.rgb(original.baseColor.tint)
            let glow = base * ambientColor
            c.emissiveColor = .init(color: Island.color(Double(glow.x), Double(glow.y), Double(glow.z)))
            c.emissiveIntensity = ambient
            return c
        }
    }

    private static func rgb(_ c: IslandColor) -> SIMD3<Float> {
        var r: CGFloat = 0, g: CGFloat = 0, b: CGFloat = 0, a: CGFloat = 0
        #if os(macOS)
        (c.usingColorSpace(.sRGB) ?? c).getRed(&r, green: &g, blue: &b, alpha: &a)
        #else
        c.getRed(&r, green: &g, blue: &b, alpha: &a)
        #endif
        return [Float(r), Float(g), Float(b)]
    }

    // MARK: Each frame

    func update(_ dt: Float) {
        t += dt
        floaters = floaters.compactMap { f in
            var f = f
            f.age += dt
            let p = min(1, f.age / f.life)
            f.velocity.y -= f.gravity * dt
            f.entity.position += f.velocity * dt
            if f.spin != 0 { f.entity.orientation = simd_quatf(angle: f.spin * dt, axis: [0, 1, 0]) * f.entity.orientation }
            f.entity.scale = Island.mix(f.from, f.to, 1 - (1 - p) * (1 - p))
            if p > f.fadeAt { Self.setOpacity(f.entity, f.opacity * (1 - (p - f.fadeAt) / max(0.001, 1 - f.fadeAt))) }
            if p >= 1 {
                f.entity.removeFromParent()
                f.then?()
                return nil
            }
            return f
        }
        stopping = stopping.filter { e, at in
            guard t >= at else { return true }
            if var p = e.components[ParticleEmitterComponent.self] {
                p.isEmitting = false
                e.components.set(p)
            }
            return false
        }
        expiring = expiring.filter { e, at in
            guard t >= at else { return true }
            e.removeFromParent()
            return false
        }
    }

    static func setOpacity(_ root: Entity, _ opacity: Float) {
        if let m = root as? ModelEntity, let materials = m.model?.materials {
            m.model?.materials = materials.map { material in
                guard var u = material as? UnlitMaterial else { return material }
                u.blending = .transparent(opacity: .init(floatLiteral: max(0, opacity)))
                return u
            }
        }
        root.children.forEach { setOpacity($0, opacity) }
    }
}
