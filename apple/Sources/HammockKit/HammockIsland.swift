import Foundation
import RealityKit
import SwiftUI

/// Hammock Island: the About screen's secret. A little low-poly island, built
/// in code (no models), with two palms, a hammock and someone asleep in it,
/// under a sky whose day goes round in two minutes (starting at noon in
/// light mode, midnight in dark).
///
/// Drag to turn it. Click the hammock to swing it: keep clicking and it loops,
/// and its sleeper goes flying (as they do if you spin the island hard
/// enough); they swim back, or lie dazed on the sand, and hop back in. Click
/// a palm to shake a coconut loose and a coconut to kick it; one landing on
/// the sleeper bonks them. Click the sea or the sand. The rest
/// (IslandSky.swift, IslandSecrets.swift) is for finding.
///
/// Nothing in it comes from the app: it's a toy of its own. In HammockKit so
/// the iPhone can have it too.
public struct HammockIslandView: View {
    @State private var island: IslandScene
    @State private var dragged: CGSize = .zero
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.colorScheme) private var colorScheme

    /// `icon`: the app's icon, which turns up here and there.
    public init(icon: CGImage? = nil) {
        _island = State(initialValue: IslandScene(icon: icon))
    }

    public var body: some View {
        let dark = colorScheme == .dark
        RealityView { content in
            await island.build(into: content, dark: dark)
        }
        .gesture(SpatialTapGesture().targetedToAnyEntity().onEnded { value in
            island.tapped(value.entity, at: value.location)
        })
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
        .background(IslandSkyBackdrop(state: island.sky.state))
    }
}

/// The island's entities and the bits of simulation RealityKit doesn't do
/// for us: the hammock's pendulum, the sleeper's adventures, the palms
/// shaking, coconuts floating, foam.
@MainActor
final class IslandScene {
    var reduceMotion = false {
        didSet { sky.reduceMotion = reduceMotion }
    }

    let effects = IslandEffects()
    let sky: IslandSky
    private let bottle: IslandBottle
    private let crab: IslandCrab
    private let icon: TextureResource?

    private let world = Entity()
    // Everything that turns when dragged; physics runs in its space, so the
    // coconuts turn with the island.
    private let turntable = Entity()
    private let camera = PerspectiveCamera()
    private var content: RealityViewCameraContent?
    private let pivot = Entity()
    private let sleeper = Entity()
    private var head = Entity()
    private var foam: [ModelEntity] = []
    private var subscriptions: [EventSubscription] = []
    private var built = false

    private var t: Float = 0
    private var yaw: Float = -0.35
    private var spin: Float = 0
    private var dragging = false
    private var elevation: Float = 0.2

    // The hammock, a pendulum about the line between the palms.
    private var angle: Float = 0
    private var swingVelocity: Float = 0
    private var bonk: Float = 0
    // Loops over the top in a row (each within a moment of the last).
    private var loops = 0
    private var lastLoop: Float = -10

    // The sleeper, who doesn't always stay in it.
    private enum Sleeper {
        case napping
        case rolling(Float)
        case flying(Float)
        case dazed(Float)
        case swimming
        case hopping(Float, Transform)
    }
    private var sleeperState: Sleeper = .napping
    private var sleeperRest = Transform()
    private var sleeperShape = ShapeResource.generateBox(width: 0.8, height: 0.2, depth: 0.26)
    private var dazeStars: Entity?
    private var nextRoll: Float = 25
    private var nextZ: Float = 1
    private var zCount = 0
    private var lastSwimRipple: Float = 0

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

    private let env = ProcessInfo.processInfo.environment
    private var debugPlay: Bool { env["HMK_DEBUG_ISLAND_PLAY"] == "1" }
    private var debugStep = 0

    // Sizes, in metres (near enough).
    private let seaY: Float = -0.14
    private var waterline: Float { seaY + 0.016 }
    private let islandRadius: Float = 1.6
    private let sag: Float = 0.34
    private let hammockHalf: Float = 0.62
    private let hammockWidth: Float = 0.24
    private let coconutRadius: Float = 0.065
    private let rockSpot = SIMD3<Float>(1.15, 0.02, 0.55)

    init(icon: CGImage?) {
        let texture = Island.texture(icon)
        self.icon = texture
        sky = IslandSky(effects: effects, icon: texture)
        bottle = IslandBottle(effects: effects, icon: texture)
        crab = IslandCrab(effects: effects)
    }

    // MARK: Building

    func build(into content: RealityViewCameraContent, dark: Bool) async {
        guard !built else { return }
        built = true
        self.content = content

        content.add(world)
        turntable.components.set(PhysicsSimulationComponent())
        world.addChild(turntable)

        camera.camera.fieldOfViewInDegrees = 50
        content.add(camera)
        placeCamera()

        let startHour = env["HMK_DEBUG_ISLAND_HOUR"].flatMap(Float.init) ?? (dark ? 0 : 12)
        sky.build(into: world, seaY: seaY, startHour: startHour)

        // The sea, the shallows, the sand.
        let sea = ModelEntity(mesh: .generateCylinder(height: 0.04, radius: 60), materials: [Island.material(0.10, 0.52, 0.72)])
        sea.name = "sea"
        sea.position.y = seaY - 0.02
        sea.components.set(CollisionComponent(shapes: [.generateBox(width: 120, height: 0.04, depth: 120)], mode: .trigger))
        sea.components.set(InputTargetComponent())
        turntable.addChild(sea)
        let shallows = ModelEntity(mesh: .generateCylinder(height: 0.04, radius: islandRadius + 0.35), materials: [Island.material(0.36, 0.80, 0.82)])
        shallows.name = "sea"
        shallows.position.y = seaY - 0.01
        turntable.addChild(shallows)
        // Foam lapping at the shore, glowing a little by night.
        for _ in 0..<2 {
            let ring = ModelEntity(mesh: Island.ringMesh, materials: [Island.unlit(1, 1, 1, opacity: 0.5)])
            ring.position.y = waterline - 0.002
            turntable.addChild(ring)
            foam.append(ring)
        }

        let sandMesh = MeshResource.generateCylinder(height: 0.3, radius: islandRadius)
        let sand = ModelEntity(mesh: sandMesh, materials: [Island.material(0.96, 0.86, 0.62)])
        sand.name = "sand"
        sand.position.y = -0.15
        sand.scale = [1, 1, 0.82]
        let sandShape = (try? await ShapeResource.generateConvex(from: sandMesh))
            ?? .generateBox(width: islandRadius * 2, height: 0.3, depth: islandRadius * 2)
        sand.components.set(CollisionComponent(shapes: [sandShape]))
        sand.components.set(InputTargetComponent())
        sand.components.set(PhysicsBodyComponent(shapes: [sandShape], mass: 0, material: .generate(staticFriction: 0.8, dynamicFriction: 0.6, restitution: 0.5), mode: .static))
        turntable.addChild(sand)
        // A sandy hump in the middle, and a few rocks (one with a lodger).
        let hump = ModelEntity(mesh: .generateSphere(radius: 1), materials: [Island.material(0.94, 0.83, 0.58)])
        hump.name = "sand"
        hump.scale = [1.25, 0.12, 0.95]
        hump.position = [0.15, -0.04, 0.05]
        turntable.addChild(hump)
        for (i, (p, r)) in [(rockSpot, Float(0.11)), ([1.3, 0.0, 0.3], 0.07), ([-1.2, 0.0, 0.6], 0.09)].enumerated() {
            let rock = ModelEntity(mesh: .generateSphere(radius: r), materials: [Island.material(0.55, 0.55, 0.58)])
            rock.position = p
            rock.scale = [1.2, 0.7, 1]
            if i == 0 {
                rock.name = "rock"
                rock.components.set(CollisionComponent(shapes: [.generateSphere(radius: r * 1.2)], mode: .trigger))
                rock.components.set(InputTargetComponent())
            }
            turntable.addChild(rock)
        }

        // Two palms leaning apart, and the hammock between them.
        let attachLeft = buildPalm(at: [-0.95, 0, -0.15], lean: 1, phase: 0)
        _ = buildPalm(at: [0.95, 0, -0.15], lean: -1, phase: 1.7)
        buildHammock(between: attachLeft)

        effects.tint(world)

        // A coconut landing on the sleeper.
        subscriptions.append(content.subscribe(to: CollisionEvents.Began.self, on: sleeper) { [weak self] event in
            MainActor.assumeIsolated {
                guard let self, case .napping = self.sleeperState,
                      event.entityA.name == "coconut" || event.entityB.name == "coconut" else { return }
                self.bonk = 1
                self.swingVelocity += self.swingVelocity >= 0 ? 1.4 : -1.4
                self.effects.burst(in: self.head, at: [0, 0.1, 0], colors: [Island.color(1, 0.95, 0.4)],
                                   count: 12, speed: 0.6, size: 0.015, life: 0.6, gravity: 0, spread: .pi)
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
        for i in 0..<segments {
            let dir = simd_quatf(angle: tilt, axis: [0, 0, 1]).act([0, 1, 0])
            let radius = 0.075 - Float(i) * 0.004
            let seg = ModelEntity(mesh: .generateCylinder(height: length * 1.05, radius: radius),
                                  materials: [i % 2 == 0 ? Island.material(0.55, 0.38, 0.22) : Island.material(0.47, 0.31, 0.18)])
            seg.position = point + dir * (length / 2)
            seg.orientation = simd_quatf(from: [0, 1, 0], to: dir)
            seg.components.set(tap)
            // RealityKit only sends taps to entities with one of these.
            seg.components.set(InputTargetComponent())
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
            let leaf = ModelEntity(mesh: Island.leafMesh, materials: [Island.material(0.20, 0.62, 0.28), Island.material(0.14, 0.50, 0.22)])
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
        let nut = ModelEntity(mesh: .generateSphere(radius: coconutRadius), materials: [Island.material(0.36, 0.24, 0.14)])
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

        let cloth = ModelEntity(mesh: Island.hammockMesh(half: hammockHalf, width: hammockWidth, sag: sag),
                                materials: [Island.material(0.98, 0.93, 0.80), Island.material(0.93, 0.42, 0.36), Island.material(0.20, 0.60, 0.66)])
        pivot.addChild(cloth)
        // Taps anywhere on it.
        pivot.components.set(CollisionComponent(shapes: [ShapeResource.generateBox(width: hammockHalf * 2, height: sag + 0.2, depth: 0.55)
                .offsetBy(translation: [0, -sag / 2, 0])], mode: .trigger))
        pivot.components.set(InputTargetComponent())

        // Spreader bars at the ends, and ropes out to the palms.
        for side: Float in [-1, 1] {
            let bar = ModelEntity(mesh: .generateCylinder(height: 0.2, radius: 0.012), materials: [Island.material(0.55, 0.38, 0.22)])
            bar.orientation = simd_quatf(angle: .pi / 2, axis: [1, 0, 0])
            bar.position = [side * hammockHalf, 0, 0]
            pivot.addChild(bar)
            let ropeLength = span - hammockHalf
            let rope = ModelEntity(mesh: .generateCylinder(height: ropeLength, radius: 0.008), materials: [Island.material(0.85, 0.80, 0.68)])
            rope.orientation = simd_quatf(angle: .pi / 2, axis: [0, 0, 1])
            rope.position = pivot.position + [side * (hammockHalf + ropeLength / 2), 0, 0]
            turntable.addChild(rope)
        }

        // The sleeper: a blanket, a head, a nightcap.
        func clothY(_ x: Float) -> Float { -sag * (1 - (x / hammockHalf) * (x / hammockHalf)) }
        sleeper.name = "sleeper"
        sleeper.position = [0, clothY(0) + 0.07, 0]
        pivot.addChild(sleeper)
        sleeperRest = sleeper.transform
        let body = ModelEntity(mesh: .generateBox(width: 0.62, height: 0.12, depth: 0.24, cornerRadius: 0.05),
                               materials: [Island.material(0.40, 0.42, 0.85)])
        body.position = [-0.06, 0.0, 0]
        body.orientation = simd_quatf(angle: -0.06, axis: [0, 0, 1])
        sleeper.addChild(body)
        let feet = ModelEntity(mesh: .generateSphere(radius: 0.07), materials: [Island.material(0.40, 0.42, 0.85)])
        feet.position = [-0.34, 0.05, 0]
        sleeper.addChild(feet)
        let headEntity = Entity()
        headEntity.position = [0.33, clothY(0.33) - clothY(0) + 0.06, 0]
        sleeper.addChild(headEntity)
        head = headEntity
        let face = ModelEntity(mesh: .generateSphere(radius: 0.085), materials: [Island.material(0.96, 0.78, 0.62)])
        headEntity.addChild(face)
        let cap = ModelEntity(mesh: .generateCone(height: 0.2, radius: 0.075), materials: [Island.material(0.90, 0.25, 0.30)])
        cap.position = [0.09, 0.04, 0]
        cap.orientation = simd_quatf(angle: -1.2, axis: [0, 0, 1])
        headEntity.addChild(cap)
        let pom = ModelEntity(mesh: .generateSphere(radius: 0.03), materials: [Island.material(1, 1, 1)])
        pom.position = [0.19, 0.08, 0]
        headEntity.addChild(pom)

        sleeperShape = ShapeResource.generateBox(width: 0.8, height: 0.2, depth: 0.26).offsetBy(translation: [0, 0.03, 0])
        sleeper.components.set(CollisionComponent(shapes: [sleeperShape]))
        sleeper.components.set(InputTargetComponent())
        sleeper.components.set(PhysicsBodyComponent(shapes: [sleeperShape], mass: 0, mode: .kinematic))
    }

    private func placeCamera() {
        let distance: Float = 4.3
        let target = SIMD3<Float>(0, 0.45, 0)
        camera.position = target + [0, sin(elevation) * distance, cos(elevation) * distance]
        camera.look(at: target, from: camera.position, relativeTo: nil)
    }

    // MARK: Input

    func tapped(_ entity: Entity, at location: CGPoint) {
        var e: Entity? = entity
        while let current = e {
            if sky.tapped(current) { return }
            if bottle.tapped(current, in: turntable) { return }
            if crab.tapped(current, in: turntable, rock: rockSpot, radius: islandRadius, seaY: seaY) { return }
            switch current.name {
            case "sleeper": return pokeSleeper()
            case "hammock": return swing()
            case "coconut": if let nut = current as? ModelEntity { return kick(nut) }
            case "sea":
                if let p = point(at: location, height: waterline) {
                    effects.ripple(in: turntable, at: p, size: 0.4)
                    effects.burst(in: turntable, at: p, colors: [Island.color(1, 1, 1), Island.color(0.6, 0.9, 1)],
                                  count: 14, speed: 0.7, size: 0.018, life: 0.5, gravity: 4, spread: 0.5)
                }
                return
            case "sand":
                if let p = point(at: location, height: 0.01) {
                    effects.burst(in: turntable, at: p, colors: [Island.color(0.96, 0.86, 0.62), Island.color(0.86, 0.74, 0.5)],
                                  count: 18, speed: 0.7, size: 0.016, life: 0.6, gravity: 4, spread: 0.6)
                }
                return
            default:
                if current.name.hasPrefix("palm-"), let i = Int(current.name.dropFirst(5)) { return shakePalm(i) }
            }
            e = current.parent
        }
    }

    /// Where a click at `location` meets the level `height` (in the
    /// island's space).
    private func point(at location: CGPoint, height: Float) -> SIMD3<Float>? {
        guard let content, let ray = content.ray(through: location, in: .local, to: .scene) else { return nil }
        let origin = turntable.convert(position: ray.origin, from: nil)
        let direction = turntable.convert(direction: ray.direction, from: nil)
        guard abs(direction.y) > 0.0001 else { return nil }
        let d = (height - origin.y) / direction.y
        return d > 0 ? origin + direction * d : nil
    }

    func drag(dx: Float, dy: Float) {
        dragging = true
        spin = 0
        yaw += dx * 0.01
        elevation = min(1.0, max(0.04, elevation + dy * 0.004))
        placeCamera()
    }

    func fling(_ velocity: Float) {
        dragging = false
        spin = max(-7, min(7, velocity * 0.01))
    }

    /// Pushes the hammock along the way it's already going: keep clicking
    /// and it goes all the way round.
    func swing() {
        let push: Float = reduceMotion ? 1.2 : 2.2
        swingVelocity += swingVelocity >= 0 ? push : -push
        swingVelocity = max(-11, min(11, swingVelocity))
    }

    /// Clicking the sleeper: in the hammock, a push; anywhere else, a fuss.
    private func pokeSleeper() {
        switch sleeperState {
        case .napping, .rolling: swing()
        case .swimming:
            let p = sleeper.position
            effects.splash(in: turntable, at: [p.x, waterline, p.z])
        case .dazed, .flying, .hopping:
            effects.burst(in: head, at: [0, 0.12, 0], colors: [Island.color(1, 0.95, 0.4), Island.color(1, 1, 1)],
                          count: 16, speed: 0.6, size: 0.016, life: 0.6, gravity: 0, spread: .pi)
        }
    }

    func shakePalm(_ i: Int) {
        guard palms.indices.contains(i) else { return }
        palms[i].shake = reduceMotion ? 0.05 : 0.14
        guard let slot = palms[i].slots.firstIndex(where: { $0 != nil }), let hanging = palms[i].slots[slot] else { return }
        palms[i].slots[slot] = nil
        palms[i].regrow[slot] = t + 3
        let world = hanging.position(relativeTo: turntable)
        hanging.removeFromParent()

        let nut = ModelEntity(mesh: .generateSphere(radius: coconutRadius), materials: [Island.material(0.36, 0.24, 0.14)])
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
        effects.tint(nut)
        coconuts.append(Coconut(entity: nut))
        if coconuts.count > 24 { coconuts.removeFirst().entity.removeFromParent() }
    }

    private func kick(_ nut: ModelEntity) {
        guard let i = coconuts.firstIndex(where: { $0.entity === nut }), coconuts[i].floating == nil else { return }
        nut.components.set(PhysicsMotionComponent(linearVelocity: [Float.random(in: -1...1), 2.2, Float.random(in: -1...1)],
                                                  angularVelocity: [Float.random(in: -8...8), 0, Float.random(in: -8...8)]))
    }

    // MARK: The sleeper's adventures

    private func throwSleeper(_ velocity: SIMD3<Float>) {
        sleeper.setParent(turntable, preservingWorldTransform: true)
        head.scale = .one
        var body = PhysicsBodyComponent(shapes: [sleeperShape], mass: 2,
                                        material: .generate(staticFriction: 0.8, dynamicFriction: 0.6, restitution: 0.3), mode: .dynamic)
        body.angularDamping = 0.6
        sleeper.components.set(body)
        var v = velocity
        if length(v) > 8.5 { v = normalize(v) * 8.5 }
        sleeper.components.set(PhysicsMotionComponent(linearVelocity: v,
                                                      angularVelocity: [Float.random(in: -5...5), Float.random(in: -3...3), Float.random(in: -5...5)]))
        sleeperState = .flying(0)
        swingVelocity *= 0.3 // they took most of the swing with them
    }

    private func stopPhysics() {
        sleeper.components.remove(PhysicsBodyComponent.self)
        sleeper.components.remove(PhysicsMotionComponent.self)
    }

    private func startHop() {
        stopPhysics()
        dazeStars?.removeFromParent()
        dazeStars = nil
        sleeperState = .hopping(0, sleeper.transform)
    }

    private func updateSleeper(_ dt: Float) {
        switch sleeperState {
        case .napping:
            // They hang on through one loop of the hammock; a second in a
            // row, or a really hard spin of the island, and off they go, far.
            if loops >= 2 {
                loops = 0
                let r = sleeper.position(relativeTo: turntable) - pivot.position
                // Over the top the hammock's slow, so the throw has a
                // floor: always well out to sea, the way it was going.
                let along = swingVelocity * cross([1, 0, 0], r)
                let flat = SIMD3<Float>(0, 0, along.z == 0 ? 1 : along.z)
                throwSleeper(normalize(flat) * max(4.5, abs(along.z) * 1.4) + [0, 3.2, 0])
                return
            }
            if abs(spin) > 6 {
                let r = sleeper.position(relativeTo: turntable)
                let around = cross(SIMD3<Float>(0, spin > 0 ? 1 : -1, 0), r + [0.001, 0, 0])
                spin *= 0.5
                throwSleeper(normalize(around) * 3.8 + normalize(SIMD3(r.x, 0, r.z) + [0.001, 0, 0]) * 2.4 + [0, 3.6, 0])
                return
            }
            // Z's, while it's calm enough to sleep.
            if abs(swingVelocity) < 3 {
                nextZ -= dt
                if nextZ <= 0 {
                    nextZ = 1.5
                    zCount += 1
                    let z = ModelEntity(mesh: Island.zMeshes[zCount % 2], materials: [Island.unlit(1, 1, 1)])
                    z.components.set(BillboardComponent())
                    effects.float(z, in: turntable, at: head.position(relativeTo: turntable) + [0, 0.08, 0], life: 2.6,
                                  velocity: [0.03, 0.16, 0], from: SIMD3(repeating: 0.6), to: SIMD3(repeating: 1.4),
                                  fadeAt: 0.45, opacity: 0.9)
                }
            }
            // Now and then they roll over in their sleep.
            if !reduceMotion && abs(swingVelocity) < 1.5 {
                nextRoll -= dt
                if nextRoll <= 0 {
                    nextRoll = Float.random(in: 20...35)
                    sleeperState = .rolling(0)
                }
            }

        case .rolling(var p):
            p += dt / 1.4
            let e = Island.smoothstep(0, 1, p)
            var tr = sleeperRest
            tr.rotation = sleeperRest.rotation * simd_quatf(angle: 2 * .pi * e, axis: [1, 0, 0])
            tr.translation.y += 0.12 * sin(.pi * min(1, p))
            sleeper.transform = tr
            sleeperState = p >= 1 ? .napping : .rolling(p)
            if p >= 1 { sleeper.transform = sleeperRest }

        case .flying(var age):
            age += dt
            let p = sleeper.position
            if p.y < seaY - 0.02 {
                effects.splash(in: turntable, at: [p.x, waterline, p.z], big: true)
                stopPhysics()
                sleeper.position.y = seaY - 0.03
                sleeperState = .swimming
                return
            }
            let speed = length(sleeper.components[PhysicsMotionComponent.self]?.linearVelocity ?? .zero)
            if age > 0.8 && speed < 0.15 {
                sleeperState = .dazed(0)
                let stars = Entity()
                for k in 0..<3 {
                    let star = ModelEntity(mesh: .generateSphere(radius: 0.018), materials: [Island.unlit(1, 0.9, 0.3)])
                    let a = Float(k) / 3 * 2 * .pi
                    star.position = [cos(a) * 0.12, 0, sin(a) * 0.12]
                    stars.addChild(star)
                }
                stars.position = [0, 0.14, 0]
                head.addChild(stars)
                dazeStars = stars
            } else if age > 9 {
                startHop()
            } else {
                sleeperState = .flying(age)
            }

        case .dazed(var age):
            age += dt
            dazeStars?.orientation = simd_quatf(angle: age * 5, axis: [0, 1, 0])
            if sleeper.position.y < seaY - 0.02 {
                dazeStars?.removeFromParent()
                dazeStars = nil
                stopPhysics()
                sleeperState = .swimming
            } else if age > 2.2 {
                startHop()
            } else {
                sleeperState = .dazed(age)
            }

        case .swimming:
            // Back to the island, the head bobbing.
            var p = sleeper.position
            let home = -normalize(SIMD3<Float>(p.x, 0, p.z) + [0.001, 0, 0])
            p += home * 0.6 * dt
            p.y = seaY - 0.04 + 0.02 * sin(t * 5)
            sleeper.position = p
            sleeper.orientation = simd_quatf(from: [1, 0, 0], to: home) * simd_quatf(angle: 0.15 * sin(t * 5), axis: [1, 0, 0])
            if t - lastSwimRipple > 0.5 {
                lastSwimRipple = t
                effects.ripple(in: turntable, at: [p.x, waterline, p.z], size: 0.3, opacity: 0.6)
            }
            if Island.reach(p, radius: islandRadius) < 1.02 { startHop() }

        case .hopping(var p, let from):
            p += dt / 1.1
            let e = Island.smoothstep(0, 1, min(1, p))
            let target = Transform(matrix: pivot.transformMatrix(relativeTo: turntable) * sleeperRest.matrix)
            var tr = Transform()
            tr.translation = Island.mix(from.translation, target.translation, e) + [0, 0.7 * sin(.pi * min(1, p)), 0]
            tr.rotation = simd_slerp(from.rotation, target.rotation, e)
            sleeper.transform = tr
            if p >= 1 {
                sleeper.setParent(pivot)
                sleeper.transform = sleeperRest
                sleeper.components.set(PhysicsBodyComponent(shapes: [sleeperShape], mass: 0, mode: .kinematic))
                swingVelocity = swingVelocity * 0.3 + 1
                bonk = 0.6
                nextZ = 1.2
                sleeperState = .napping
            } else {
                sleeperState = .hopping(p, from)
            }
        }
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

        sky.eye = camera.position
        sky.update(dt, seaY: seaY)
        let ambient = sky.ambient
        effects.setAmbient(ambient.intensity, color: ambient.color)
        effects.update(dt)
        bottle.update(dt, in: turntable, seaY: seaY, radius: islandRadius)
        crab.update(dt, in: turntable, seaY: seaY)

        // The pendulum, in small steps so a big swing stays stable. Empty,
        // it's lighter and swings more freely.
        let steps = 4
        let h = dt / Float(steps)
        let drive: Float = reduceMotion ? 0 : 0.12 * sin(t * 2.4)
        let damping: Float = reduceMotion ? 1.2 : 0.35
        for _ in 0..<steps {
            swingVelocity += (-6 * sin(angle) - damping * swingVelocity + drive) * h
            angle += swingVelocity * h
        }
        var looped = false
        if angle > .pi { angle -= 2 * .pi; looped = true } else if angle < -.pi { angle += 2 * .pi; looped = true }
        if looped {
            loops = t - lastLoop < 2.5 ? loops + 1 : 1
            lastLoop = t
        }
        pivot.orientation = simd_quatf(angle: angle, axis: [1, 0, 0])

        updateSleeper(dt)

        // Bonked: the head squashes and springs back.
        if bonk > 0 {
            bonk = max(0, bonk - dt * 2.5)
            let s = 1 - 0.35 * bonk * abs(cos(bonk * 12))
            head.scale = [1 / s, s, 1 / s]
        }

        // Foam: rings spreading from the shore and fading; by night, faintly aglow.
        let glow = 1 - sky.daylight
        for (i, ring) in foam.enumerated() {
            let phase = (t / 3.4 + Float(i) * 0.5).truncatingRemainder(dividingBy: 1)
            let r = islandRadius * (1.01 + 0.17 * phase)
            ring.scale = [r, 1, r * 0.82]
            let c = Island.mix(SIMD3<Float>(1, 1, 1), [0.45, 1, 0.9], glow)
            var m = Island.glow(Island.color(Double(c.x), Double(c.y), Double(c.z)))
            m.blending = .transparent(opacity: .init(floatLiteral: (0.6 + 0.3 * glow) * (1 - phase) * min(1, phase * 5)))
            ring.model?.materials = [m]
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
                effects.tint(nut)
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

        // Coconuts that roll into the sea splash, float off and sink.
        coconuts = coconuts.compactMap { c in
            var c = c
            c.age += dt
            if c.floating == nil, c.entity.position.y < seaY + coconutRadius * 0.3 {
                c.entity.components.remove(PhysicsBodyComponent.self)
                c.entity.components.remove(PhysicsMotionComponent.self)
                c.floating = 0
                let p = c.entity.position
                effects.splash(in: turntable, at: [p.x, waterline, p.z])
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

    /// HMK_DEBUG_ISLAND_PLAY=1: plays a take by itself, for captures: a
    /// bottle and a boat early, coconuts, and swings until the sleeper flies.
    private func playDebugTake() {
        let take: [(Float, () -> Void)] = [
            (0.2, { self.bottle.debugSoon(); self.sky.debugBoatSoon() }),
            (1.0, { self.shakePalm(0) }), (1.4, { self.swing() }), (1.9, { self.swing() }),
            (2.2, { self.shakePalm(1) }), (2.5, { self.swing() }), (3.0, { self.swing() }),
            (3.5, { self.swing() }), (4.0, { self.swing() }), (4.5, { self.swing() }),
            (5.0, { self.swing() }), (5.4, { self.swing() }), (5.8, { self.swing() }), (6.2, { self.swing() }),
        ]
        while debugStep < take.count, t >= take[debugStep].0 {
            take[debugStep].1()
            debugStep += 1
        }
    }
}
