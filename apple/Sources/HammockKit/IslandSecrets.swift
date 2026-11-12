import RealityKit
import SwiftUI

/// A message in a bottle that drifts in now and then and washes up on the
/// sand. Click it: the cork pops and the app's icon rises out of it.
@MainActor
final class IslandBottle {
    private enum State {
        case waiting(until: Float)
        case drifting(angle: Float, reach: Float)
        case beached(angle: Float)
    }
    private var state: State = .waiting(until: 12)
    private var bottle: Entity?
    private let effects: IslandEffects
    private let icon: TextureResource?
    private var t: Float = 0

    init(effects: IslandEffects, icon: TextureResource?) {
        self.effects = effects
        self.icon = icon
    }

    func debugSoon() { state = .waiting(until: 1) }

    private func makeBottle() -> Entity {
        let e = Entity()
        e.name = "bottle"
        let model = Entity()
        model.orientation = simd_quatf(angle: .pi / 2, axis: [0, 0, 1])
        let glass = Island.material(0.45, 0.78, 0.55, opacity: 0.6)
        let body = ModelEntity(mesh: .generateCylinder(height: 0.17, radius: 0.045), materials: [glass])
        model.addChild(body)
        let neck = ModelEntity(mesh: .generateCylinder(height: 0.07, radius: 0.02), materials: [glass])
        neck.position.y = 0.115
        model.addChild(neck)
        let cork = ModelEntity(mesh: .generateCylinder(height: 0.035, radius: 0.022), materials: [Island.material(0.66, 0.48, 0.30)])
        cork.name = "cork"
        cork.position.y = 0.16
        model.addChild(cork)
        let paper = ModelEntity(mesh: .generateCylinder(height: 0.11, radius: 0.028), materials: [Island.material(0.98, 0.95, 0.85)])
        model.addChild(paper)
        e.addChild(model)
        e.components.set(CollisionComponent(shapes: [.generateBox(width: 0.34, height: 0.14, depth: 0.14)], mode: .trigger))
        e.components.set(InputTargetComponent())
        return e
    }

    func update(_ dt: Float, in turntable: Entity, seaY: Float, radius: Float) {
        t += dt
        switch state {
        case .waiting(let until):
            guard t >= until else { return }
            let b = makeBottle()
            turntable.addChild(b)
            effects.tint(b)
            bottle = b
            state = .drifting(angle: Float.random(in: 0..<(2 * .pi)), reach: 3.2)
        case .drifting(let angle, var reach):
            guard let bottle else { return }
            reach -= 0.2 * dt
            let landing = Island.smoothstep(1.12, 0.94, reach)
            var p = Island.rim(angle, radius: radius, scale: reach)
            p.y = Island.mix(seaY + 0.03 + 0.02 * sin(t * 2.2), 0.045, landing)
            bottle.position = p
            bottle.orientation = simd_quatf(angle: -angle + 1.2, axis: [0, 1, 0])
                * simd_quatf(angle: 0.25 * sin(t * 1.6) * (1 - landing), axis: [1, 0, 0])
            if Int(t * 2) != Int((t - dt) * 2) && landing < 0.5 {
                effects.ripple(in: turntable, at: [p.x, seaY + 0.016, p.z], size: 0.18, opacity: 0.5)
            }
            state = reach <= 0.94 ? .beached(angle: angle) : .drifting(angle: angle, reach: reach)
        case .beached:
            break
        }
    }

    func tapped(_ entity: Entity, in turntable: Entity) -> Bool {
        guard entity.name == "bottle", let bottle, entity === bottle else { return false }
        if case .waiting = state { return false }
        let p = bottle.position(relativeTo: turntable)
        // The cork flies off.
        let cork = ModelEntity(mesh: .generateCylinder(height: 0.035, radius: 0.022), materials: [Island.material(0.66, 0.48, 0.30)])
        effects.tint(cork)
        effects.float(cork, in: turntable, at: p + [0, 0.05, 0], life: 1.2,
                      velocity: [Float.random(in: -0.5...0.5), 2.4, Float.random(in: -0.3...0.3)], gravity: 6, spin: 14,
                      to: SIMD3(repeating: 0.6))
        effects.burst(in: turntable, at: p + [0, 0.08, 0], colors: [Island.color(1, 0.9, 0.4), Island.color(1, 1, 1)],
                      count: 50, speed: 1.4, size: 0.02, life: 1.0, gravity: 1, spread: 0.7)
        // The message: the icon, rising and turning.
        let message: Entity
        if let icon {
            message = Island.card(icon, size: 0.12)
        } else {
            message = ModelEntity(mesh: Island.text("hi!", size: 0.1), materials: [Island.unlit(1, 1, 1)])
        }
        effects.float(message, in: turntable, at: p + [0, 0.15, 0], life: 3.4, velocity: [0, 0.32, 0], spin: 2.6,
                      from: SIMD3(repeating: 0.3), to: SIMD3(repeating: 4.2), fadeAt: 0.6)
        // The bottle shrinks away.
        effects.float(bottle, in: turntable, at: p, life: 0.7, to: SIMD3(repeating: 0.01))
        self.bottle = nil
        state = .waiting(until: t + Float.random(in: 35...55))
        return true
    }
}

/// A crab under the big rock. Click the rock and it scuttles out, sideways,
/// across the sand and into the sea. Click the crab and it jumps.
@MainActor
final class IslandCrab {
    private var crab: Entity?
    private var path: [SIMD3<Float>] = []
    private var leg = 0
    private var progress: Float = 0
    private var hop: Float = 0
    private var hopVelocity: Float = 0
    private var claws: [Entity] = []
    private var snap: Float = 0
    private let effects: IslandEffects
    private var t: Float = 0

    init(effects: IslandEffects) { self.effects = effects }

    func tapped(_ entity: Entity, in turntable: Entity, rock: SIMD3<Float>, radius: Float, seaY: Float) -> Bool {
        if entity.name == "crab" {
            hopVelocity = 1.6
            snap = 1
            return true
        }
        guard entity.name == "rock" else { return false }
        guard crab == nil else { return true }
        let c = makeCrab()
        turntable.addChild(c)
        effects.tint(c)
        crab = c
        effects.burst(in: turntable, at: rock + [0, 0.03, 0], colors: [Island.color(0.96, 0.86, 0.62), Island.color(0.85, 0.75, 0.5)],
                      count: 20, speed: 0.6, size: 0.015, life: 0.6, gravity: 3, spread: 0.9)
        // Out from under the rock, a zig and a zag, then off into the sea.
        let out = atan2(rock.z / 0.82, rock.x)
        let wander = Float.random(in: -1...1)
        path = [
            rock,
            Island.rim(out + 0.5 * wander, radius: radius, scale: 0.55),
            Island.rim(out - 0.4 * wander, radius: radius, scale: 0.8),
            Island.rim(out + 0.2, radius: radius, scale: 1.25),
        ].map { [$0.x, 0.03, $0.z] }
        path[path.count - 1].y = seaY - 0.1
        leg = 0
        progress = 0
        return true
    }

    private func makeCrab() -> Entity {
        let c = Entity()
        c.name = "crab"
        let red = Island.material(0.92, 0.36, 0.26)
        let body = ModelEntity(mesh: .generateSphere(radius: 0.06), materials: [red])
        body.scale = [1.35, 0.55, 1]
        c.addChild(body)
        claws = []
        for side: Float in [-1, 1] {
            let claw = ModelEntity(mesh: .generateSphere(radius: 0.026), materials: [red])
            claw.position = [side * 0.075, 0.02, 0.06]
            claw.scale = [1, 0.8, 1.3]
            c.addChild(claw)
            claws.append(claw)
            let eye = ModelEntity(mesh: .generateSphere(radius: 0.013), materials: [Island.material(1, 1, 1)])
            eye.position = [side * 0.025, 0.05, 0.035]
            c.addChild(eye)
            let pupil = ModelEntity(mesh: .generateSphere(radius: 0.007), materials: [Island.material(0.05, 0.05, 0.05)])
            pupil.position = [side * 0.025, 0.052, 0.046]
            c.addChild(pupil)
            for k in 0..<3 {
                let legEntity = ModelEntity(mesh: .generateBox(width: 0.05, height: 0.008, depth: 0.008), materials: [red])
                legEntity.position = [side * 0.075, -0.01, Float(k - 1) * 0.025]
                legEntity.orientation = simd_quatf(angle: side * -0.5, axis: [0, 0, 1])
                c.addChild(legEntity)
            }
        }
        c.components.set(CollisionComponent(shapes: [.generateBox(width: 0.22, height: 0.1, depth: 0.16)], mode: .trigger))
        c.components.set(InputTargetComponent())
        return c
    }

    func update(_ dt: Float, in turntable: Entity, seaY: Float) {
        t += dt
        guard let crab, leg < path.count - 1 else { return }
        let a = path[leg], b = path[leg + 1]
        let span = max(0.05, simd_distance(a, b))
        progress += dt * 0.55 / span
        hopVelocity -= 7 * dt
        hop = max(0, hop + hopVelocity * dt)
        if hop == 0 { hopVelocity = max(0, hopVelocity) }
        snap = max(0, snap - dt * 1.5)
        for (i, claw) in claws.enumerated() {
            claw.position.y = 0.02 + 0.05 * snap * abs(sin(t * 14 + Float(i)))
        }
        var p = Island.mix(a, b, min(1, progress))
        p.y += 0.008 * abs(sin(t * 28)) + hop
        crab.position = p
        // Crabs go sideways: its side faces the way it's going.
        let d = b - a
        crab.orientation = simd_quatf(angle: atan2(-d.z, d.x), axis: [0, 1, 0])
        if progress >= 1 {
            progress = 0
            leg += 1
            if leg >= path.count - 1 {
                effects.splash(in: turntable, at: [p.x, seaY + 0.016, p.z])
                crab.removeFromParent()
                self.crab = nil
            }
        }
        if leg == path.count - 2, p.y < seaY + 0.02, Int(t * 3) != Int((t - dt) * 3) {
            effects.ripple(in: turntable, at: [p.x, seaY + 0.016, p.z], size: 0.15, opacity: 0.5)
        }
    }
}
