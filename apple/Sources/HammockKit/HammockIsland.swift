import Foundation
import RealityKit
import SwiftUI

/// Hammock Island: the About screen's secret. A little low-poly island, built
/// in code (no models), with two palms, a hammock and someone asleep in it.
/// Drag to turn it, click the hammock to swing it (keep clicking), click a
/// palm to shake a coconut loose, click a fallen coconut to kick it. A
/// coconut that lands on the sleeper gives the hammock a jolt.
///
/// In HammockKit so the iPhone can have it too.
public struct HammockIslandView: View {
    @State private var island = IslandScene()
    @State private var dragged: CGSize = .zero
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.colorScheme) private var colorScheme

    public init() {}

    public var body: some View {
        RealityView { content in
            await island.build(into: content)
        }
        .gesture(SpatialTapGesture().targetedToAnyEntity().onEnded { island.tapped($0.entity) })
        .simultaneousGesture(
            DragGesture(minimumDistance: 3)
                .onChanged { value in
                    let dx = value.translation.width - dragged.width
                    let dy = value.translation.height - dragged.height
                    dragged = value.translation
                    island.drag(dx: Float(dx), dy: Float(dy))
                }
                .onEnded { value in
                    dragged = .zero
                    island.fling(Float(value.velocity.width))
                }
        )
        .onChange(of: reduceMotion, initial: true) { island.reduceMotion = reduceMotion }
        .background(sky)
    }

    private var sky: some View {
        LinearGradient(colors: colorScheme == .dark
                       ? [Color(red: 0.10, green: 0.12, blue: 0.30), Color(red: 0.55, green: 0.35, blue: 0.50)]
                       : [Color(red: 0.42, green: 0.74, blue: 0.98), Color(red: 0.86, green: 0.95, blue: 1.0)],
                       startPoint: .top, endPoint: .bottom)
    }
}

/// The island's entities and the little bits of simulation RealityKit doesn't
/// do for us: the hammock's pendulum, the palms shaking, coconuts floating.
@MainActor
final class IslandScene {
    var reduceMotion = false

    // Everything that turns when dragged; physics runs in its space, so the
    // coconuts turn with the island.
    private let turntable = Entity()
    private let camera = PerspectiveCamera()
    private let pivot = Entity()
    private let sleeper = Entity()
    private var head = Entity()
    private var subscriptions: [EventSubscription] = []
    private var built = false

    private var t: Float = 0
    private var yaw: Float = -0.35
    private var spin: Float = 0
    private var dragging = false
    private var elevation: Float = 0.32

    // The hammock, a pendulum about the line between the palms.
    private var angle: Float = 0
    private var swingVelocity: Float = 0
    private var bonk: Float = 0

    private struct Palm {
        let root: Entity
        let crown: Entity
        let crownRest: simd_quatf
        var shake: Float = 0
        var slots: [Entity?]
        var slotSpots: [SIMD3<Float>]
        var regrow: [Float?]
    }
    private var palms: [Palm] = []

    private struct Coconut {
        let entity: ModelEntity
        var age: Float = 0
        var floating: Float? = nil
    }
    private var coconuts: [Coconut] = []
    private var growing: [(Entity, Float)] = []

    private let debugPlay = ProcessInfo.processInfo.environment["HMK_DEBUG_ISLAND_PLAY"] == "1"
    private var debugStep = 0

    // Sizes, in metres (near enough).
    private let seaY: Float = -0.14
    private let islandRadius: Float = 1.6
    private let sag: Float = 0.34
    private let hammockHalf: Float = 0.62
    private let hammockWidth: Float = 0.24
    private let coconutRadius: Float = 0.065

    // MARK: Building

    func build(into content: RealityViewCameraContent) async {
        guard !built else { return }
        built = true

        turntable.components.set(PhysicsSimulationComponent())
        content.add(turntable)

        camera.camera.fieldOfViewInDegrees = 38
        content.add(camera)
        placeCamera()

        let sun = DirectionalLight()
        sun.light.intensity = 2600
        sun.light.color = .init(red: 1, green: 0.96, blue: 0.88, alpha: 1)
        sun.shadow = DirectionalLightComponent.Shadow(maximumDistance: 8, depthBias: 2)
        sun.look(at: .zero, from: [2.5, 4, 2.2], relativeTo: nil)
        content.add(sun)
        let fill = DirectionalLight()
        fill.light.intensity = 700
        fill.light.color = .init(red: 0.75, green: 0.85, blue: 1, alpha: 1)
        fill.look(at: .zero, from: [-3, 1.5, -2], relativeTo: nil)
        content.add(fill)

        // The sea, the shallows, the sand.
        let sea = ModelEntity(mesh: .generateCylinder(height: 0.04, radius: 60), materials: [Self.material(0.10, 0.52, 0.72)])
        sea.position.y = seaY - 0.02
        turntable.addChild(sea)
        let shallows = ModelEntity(mesh: .generateCylinder(height: 0.04, radius: islandRadius + 0.35), materials: [Self.material(0.36, 0.80, 0.82)])
        shallows.position.y = seaY - 0.01
        turntable.addChild(shallows)

        let sandMesh = MeshResource.generateCylinder(height: 0.3, radius: islandRadius)
        let sand = ModelEntity(mesh: sandMesh, materials: [Self.material(0.96, 0.86, 0.62)])
        sand.position.y = -0.15
        sand.scale = [1, 1, 0.82]
        let sandShape = (try? await ShapeResource.generateConvex(from: sandMesh))
            ?? .generateBox(width: islandRadius * 2, height: 0.3, depth: islandRadius * 2)
        sand.components.set(CollisionComponent(shapes: [sandShape]))
        sand.components.set(PhysicsBodyComponent(shapes: [sandShape], mass: 0, material: .generate(staticFriction: 0.8, dynamicFriction: 0.6, restitution: 0.5), mode: .static))
        turntable.addChild(sand)
        // A sandy hump in the middle, and a few rocks.
        let hump = ModelEntity(mesh: .generateSphere(radius: 1), materials: [Self.material(0.94, 0.83, 0.58)])
        hump.scale = [1.25, 0.12, 0.95]
        hump.position = [0.15, -0.04, 0.05]
        turntable.addChild(hump)
        for (p, r) in [(SIMD3<Float>(1.15, 0.02, 0.55), Float(0.11)), ([1.3, 0.0, 0.3], 0.07), ([-1.2, 0.0, 0.6], 0.09)] {
            let rock = ModelEntity(mesh: .generateSphere(radius: r), materials: [Self.material(0.55, 0.55, 0.58)])
            rock.position = p
            rock.scale = [1.2, 0.7, 1]
            turntable.addChild(rock)
        }

        // Two palms leaning apart, and the hammock between them.
        let attachLeft = buildPalm(at: [-0.95, 0, -0.15], lean: 1, phase: 0)
        _ = buildPalm(at: [0.95, 0, -0.15], lean: -1, phase: 1.7)
        buildHammock(between: attachLeft)

        // A coconut landing on the sleeper.
        subscriptions.append(content.subscribe(to: CollisionEvents.Began.self, on: sleeper) { [weak self] event in
            MainActor.assumeIsolated {
                guard let self, event.entityA.name == "coconut" || event.entityB.name == "coconut" else { return }
                self.bonk = 1
                self.swingVelocity += self.swingVelocity >= 0 ? 1.4 : -1.4
            }
        })
        subscriptions.append(content.subscribe(to: SceneEvents.Update.self) { [weak self] event in
            MainActor.assumeIsolated { self?.update(Float(event.deltaTime)) }
        })
    }

    /// A palm whose base is at `base`, leaning outward (`lean` 1 leans
    /// towards -x). Returns where the hammock's rope ties on.
    private func buildPalm(at base: SIMD3<Float>, lean: Float, phase: Float) -> SIMD3<Float> {
        let root = Entity()
        root.name = "palm-\(palms.count)"
        root.position = base
        turntable.addChild(root)

        var point = SIMD3<Float>(0, -0.05, 0)
        var tilt: Float = 0.05 * lean
        var attach = SIMD3<Float>.zero
        let segments = 9
        let length: Float = 0.2
        let tap = CollisionComponent(shapes: [.generateBox(width: 0.2, height: length, depth: 0.2)], mode: .trigger)
        let target = InputTargetComponent()
        for i in 0..<segments {
            let dir = simd_quatf(angle: tilt, axis: [0, 0, 1]).act([0, 1, 0])
            let radius = 0.075 - Float(i) * 0.004
            let seg = ModelEntity(mesh: .generateCylinder(height: length * 1.05, radius: radius),
                                  materials: [i % 2 == 0 ? Self.material(0.55, 0.38, 0.22) : Self.material(0.47, 0.31, 0.18)])
            seg.position = point + dir * (length / 2)
            seg.orientation = simd_quatf(from: [0, 1, 0], to: dir)
            seg.components.set(tap)
            seg.components.set(target)
            root.addChild(seg)
            point += dir * length
            if i == 3 { attach = base + point }
            tilt += (i < 4 ? 0.03 : 0.07) * lean
        }

        let crown = Entity()
        crown.position = point
        crown.components.set(CollisionComponent(shapes: [.generateSphere(radius: 0.45)], mode: .trigger))
        crown.components.set(InputTargetComponent())
        root.addChild(crown)
        let fronds = 8
        for k in 0..<fronds {
            let leaf = ModelEntity(mesh: Self.leafMesh, materials: [Self.material(0.20, 0.62, 0.28), Self.material(0.14, 0.50, 0.22)])
            let around = Float(k) / Float(fronds) * 2 * .pi + phase
            let up: Float = k % 2 == 0 ? 0.35 : 0.15
            leaf.orientation = simd_quatf(angle: around, axis: [0, 1, 0]) * simd_quatf(angle: up, axis: [0, 0, 1])
            crown.addChild(leaf)
        }
        let rest = crown.orientation

        var spots: [SIMD3<Float>] = []
        var slots: [Entity?] = []
        for k in 0..<3 {
            let a = Float(k) / 3 * 2 * .pi + 0.4
            let spot = SIMD3<Float>(cos(a) * 0.08, -0.07, sin(a) * 0.08)
            spots.append(spot)
            slots.append(addHangingCoconut(to: crown, at: spot))
        }
        palms.append(Palm(root: root, crown: crown, crownRest: rest, slots: slots, slotSpots: spots, regrow: Array(repeating: nil, count: 3)))
        return attach
    }

    private func addHangingCoconut(to crown: Entity, at spot: SIMD3<Float>) -> Entity {
        let nut = ModelEntity(mesh: .generateSphere(radius: coconutRadius), materials: [Self.material(0.36, 0.24, 0.14)])
        nut.position = spot
        crown.addChild(nut)
        return nut
    }

    private func buildHammock(between attach: SIMD3<Float>) {
        // The palms are mirror images, so the line between the ties runs along x.
        let span = abs(attach.x)
        pivot.name = "hammock"
        pivot.position = [0, attach.y, attach.z]
        turntable.addChild(pivot)

        let cloth = ModelEntity(mesh: Self.hammockMesh(half: hammockHalf, width: hammockWidth, sag: sag),
                                materials: [Self.material(0.98, 0.93, 0.80), Self.material(0.93, 0.42, 0.36), Self.material(0.20, 0.60, 0.66)])
        pivot.addChild(cloth)
        // Taps anywhere on it.
        pivot.components.set(CollisionComponent(shapes: [ShapeResource.generateBox(width: hammockHalf * 2, height: sag + 0.2, depth: 0.55)
                .offsetBy(translation: [0, -sag / 2, 0])], mode: .trigger))
        // RealityKit only sends taps to entities with one of these.
        pivot.components.set(InputTargetComponent())

        // Spreader bars at the ends, and ropes out to the palms.
        for side: Float in [-1, 1] {
            let bar = ModelEntity(mesh: .generateCylinder(height: 0.2, radius: 0.012), materials: [Self.material(0.55, 0.38, 0.22)])
            bar.orientation = simd_quatf(angle: .pi / 2, axis: [1, 0, 0])
            bar.position = [side * hammockHalf, 0, 0]
            pivot.addChild(bar)
            let ropeLength = span - hammockHalf
            let rope = ModelEntity(mesh: .generateCylinder(height: ropeLength, radius: 0.008), materials: [Self.material(0.85, 0.80, 0.68)])
            rope.orientation = simd_quatf(angle: .pi / 2, axis: [0, 0, 1])
            rope.position = pivot.position + [side * (hammockHalf + ropeLength / 2), 0, 0]
            turntable.addChild(rope)
        }

        // The sleeper: a blanket, a head, a nightcap.
        func clothY(_ x: Float) -> Float { -sag * (1 - (x / hammockHalf) * (x / hammockHalf)) }
        sleeper.name = "sleeper"
        sleeper.position = [0, clothY(0) + 0.07, 0]
        pivot.addChild(sleeper)
        let body = ModelEntity(mesh: .generateBox(width: 0.62, height: 0.12, depth: 0.24, cornerRadius: 0.05),
                               materials: [Self.material(0.40, 0.42, 0.85)])
        body.position = [-0.06, 0.0, 0]
        body.orientation = simd_quatf(angle: -0.06, axis: [0, 0, 1])
        sleeper.addChild(body)
        let feet = ModelEntity(mesh: .generateSphere(radius: 0.07), materials: [Self.material(0.40, 0.42, 0.85)])
        feet.position = [-0.34, 0.05, 0]
        sleeper.addChild(feet)
        let headEntity = Entity()
        headEntity.position = [0.33, clothY(0.33) - clothY(0) + 0.06, 0]
        sleeper.addChild(headEntity)
        head = headEntity
        let face = ModelEntity(mesh: .generateSphere(radius: 0.085), materials: [Self.material(0.96, 0.78, 0.62)])
        headEntity.addChild(face)
        let cap = ModelEntity(mesh: .generateCone(height: 0.2, radius: 0.075), materials: [Self.material(0.90, 0.25, 0.30)])
        cap.position = [0.09, 0.04, 0]
        cap.orientation = simd_quatf(angle: -1.2, axis: [0, 0, 1])
        headEntity.addChild(cap)
        let pom = ModelEntity(mesh: .generateSphere(radius: 0.03), materials: [Self.material(1, 1, 1)])
        pom.position = [0.19, 0.08, 0]
        headEntity.addChild(pom)

        let shape = ShapeResource.generateBox(width: 0.8, height: 0.2, depth: 0.26).offsetBy(translation: [0, 0.03, 0])
        sleeper.components.set(CollisionComponent(shapes: [shape]))
        sleeper.components.set(InputTargetComponent())
        sleeper.components.set(PhysicsBodyComponent(shapes: [shape], mass: 0, mode: .kinematic))
    }

    private func placeCamera() {
        let distance: Float = 5.0
        let target = SIMD3<Float>(0, 0.38, 0)
        camera.position = target + [0, sin(elevation) * distance, cos(elevation) * distance]
        camera.look(at: target, from: camera.position, relativeTo: nil)
    }

    // MARK: Input

    func tapped(_ entity: Entity) {
        var e: Entity? = entity
        while let current = e {
            if current.name == "hammock" || current.name == "sleeper" { return swing() }
            if current.name == "coconut", let nut = current as? ModelEntity { return kick(nut) }
            if current.name.hasPrefix("palm-"), let i = Int(current.name.dropFirst(5)) { return shakePalm(i) }
            e = current.parent
        }
    }

    func drag(dx: Float, dy: Float) {
        dragging = true
        spin = 0
        yaw += dx * 0.01
        elevation = min(1.0, max(0.08, elevation + dy * 0.004))
        placeCamera()
    }

    func fling(_ velocity: Float) {
        dragging = false
        spin = max(-6, min(6, velocity * 0.01))
    }

    /// Pushes the hammock along the way it's already going: keep clicking
    /// and it goes all the way round.
    func swing() {
        let push: Float = reduceMotion ? 1.2 : 2.2
        swingVelocity += swingVelocity >= 0 ? push : -push
        swingVelocity = max(-11, min(11, swingVelocity))
    }

    func shakePalm(_ i: Int) {
        guard palms.indices.contains(i) else { return }
        palms[i].shake = reduceMotion ? 0.05 : 0.14
        guard let slot = palms[i].slots.firstIndex(where: { $0 != nil }), let hanging = palms[i].slots[slot] else { return }
        palms[i].slots[slot] = nil
        palms[i].regrow[slot] = t + 3
        let world = hanging.position(relativeTo: turntable)
        hanging.removeFromParent()

        let nut = ModelEntity(mesh: .generateSphere(radius: coconutRadius), materials: [Self.material(0.36, 0.24, 0.14)])
        nut.name = "coconut"
        nut.position = world
        let shape = ShapeResource.generateSphere(radius: coconutRadius)
        nut.components.set(CollisionComponent(shapes: [shape]))
        nut.components.set(InputTargetComponent())
        var body = PhysicsBodyComponent(shapes: [shape], mass: 0.4,
                                        material: .generate(staticFriction: 0.6, dynamicFriction: 0.4, restitution: 0.55), mode: .dynamic)
        body.angularDamping = 0.8
        nut.components.set(body)
        nut.components.set(PhysicsMotionComponent(linearVelocity: [Float.random(in: -0.4...0.4), 0.3, Float.random(in: -0.2...0.5)]))
        turntable.addChild(nut)
        coconuts.append(Coconut(entity: nut))
        if coconuts.count > 24 { coconuts.removeFirst().entity.removeFromParent() }
    }

    private func kick(_ nut: ModelEntity) {
        guard let i = coconuts.firstIndex(where: { $0.entity === nut }), coconuts[i].floating == nil else { return }
        nut.components.set(PhysicsMotionComponent(linearVelocity: [Float.random(in: -1...1), 2.2, Float.random(in: -1...1)],
                                                  angularVelocity: [Float.random(in: -8...8), 0, Float.random(in: -8...8)]))
    }

    // MARK: Each frame

    private func update(_ dt: Float) {
        let dt = min(dt, 1 / 20)
        t += dt
        if debugPlay { playDebugTake() }

        if !dragging {
            yaw += spin * dt
            spin *= pow(0.15, dt)
            if !reduceMotion && abs(spin) < 0.02 { yaw += 0.04 * dt } // a slow turn on its own
        }
        turntable.orientation = simd_quatf(angle: yaw, axis: [0, 1, 0])

        // The pendulum, in small steps so a big swing stays stable.
        let steps = 4
        let h = dt / Float(steps)
        let drive: Float = reduceMotion ? 0 : 0.12 * sin(t * 2.4)
        let damping: Float = reduceMotion ? 1.2 : 0.35
        for _ in 0..<steps {
            swingVelocity += (-6 * sin(angle) - damping * swingVelocity + drive) * h
            angle += swingVelocity * h
        }
        if angle > .pi { angle -= 2 * .pi } else if angle < -.pi { angle += 2 * .pi }
        pivot.orientation = simd_quatf(angle: angle, axis: [1, 0, 0])

        // Bonked: the head squashes and springs back.
        if bonk > 0 {
            bonk = max(0, bonk - dt * 2.5)
            let s = 1 - 0.35 * bonk * abs(cos(bonk * 12))
            head.scale = [1 / s, s, 1 / s]
        }

        for i in palms.indices {
            palms[i].shake *= pow(0.03, dt)
            let idle: Float = reduceMotion ? 0 : 0.025 * sin(t * 1.3 + Float(i) * 1.7)
            let wobble = palms[i].shake * sin(t * 24)
            palms[i].crown.orientation = palms[i].crownRest * simd_quatf(angle: idle + wobble, axis: [0, 0, 1])
            for slot in palms[i].regrow.indices {
                guard let at = palms[i].regrow[slot], t >= at else { continue }
                palms[i].regrow[slot] = nil
                let nut = addHangingCoconut(to: palms[i].crown, at: palms[i].slotSpots[slot])
                nut.scale = [0.01, 0.01, 0.01]
                palms[i].slots[slot] = nut
                growing.append((nut, 0))
            }
        }
        growing = growing.compactMap { nut, progress in
            let p = min(1, progress + dt / 0.6)
            nut.scale = SIMD3(repeating: max(0.01, p + 0.3 * sin(p * .pi))) // a little overshoot
            return p < 1 ? (nut, p) : nil
        }

        // Coconuts that roll into the sea float off and sink.
        coconuts = coconuts.compactMap { c in
            var c = c
            c.age += dt
            if c.floating == nil, c.entity.position.y < seaY + coconutRadius * 0.3 {
                c.entity.components.remove(PhysicsBodyComponent.self)
                c.entity.components.remove(PhysicsMotionComponent.self)
                c.floating = 0
            }
            if var f = c.floating {
                f += dt
                c.floating = f
                var p = c.entity.position
                let out = normalize(SIMD3<Float>(p.x, 0, p.z) + [0.001, 0, 0])
                p += out * 0.12 * dt
                p.y = seaY + coconutRadius * 0.2 + 0.015 * sin(f * 3) - max(0, f - 5) * 0.06
                c.entity.position = p
                if f > 7 { c.entity.removeFromParent(); return nil }
            }
            return c
        }
    }

    /// HMK_DEBUG_ISLAND_PLAY=1: swings the hammock and drops coconuts by
    /// itself, for captures.
    private func playDebugTake() {
        let take: [(Float, () -> Void)] = [
            (1.0, { self.shakePalm(0) }), (1.4, { self.swing() }), (1.9, { self.swing() }),
            (2.2, { self.shakePalm(1) }), (2.5, { self.swing() }), (3.0, { self.shakePalm(0) }),
        ]
        while debugStep < take.count, t >= take[debugStep].0 {
            take[debugStep].1()
            debugStep += 1
        }
    }

    // MARK: Meshes and materials

    /// Flat, matte colour with a little glow of its own, so shadows stay
    /// cartoon-soft without an environment light.
    static func material(_ r: Double, _ g: Double, _ b: Double) -> PhysicallyBasedMaterial {
        var m = PhysicallyBasedMaterial()
        #if os(macOS)
        let color = NSColor(red: r, green: g, blue: b, alpha: 1)
        #else
        let color = UIColor(red: r, green: g, blue: b, alpha: 1)
        #endif
        m.baseColor = .init(tint: color)
        m.roughness = 0.9
        m.metallic = 0.0
        m.emissiveColor = .init(color: color)
        m.emissiveIntensity = 0.28
        return m
    }

    /// A drooping palm frond along +x, two-tone down its middle.
    static let leafMesh: MeshResource = surface(nu: 10, nv: 4, material: { _, j in j < 2 ? 0 : 1 }) { u, v in
        let length: Float = 0.75
        let width = 0.13 * pow(sin(.pi * min(1, u * 1.15)), 0.7) + 0.005
        let across = v * 2 - 1
        return [u * length, -0.42 * u * u - 0.03 * abs(across), across * width]
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
}
