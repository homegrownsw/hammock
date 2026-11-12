import RealityKit
import SwiftUI

/// The sky's colours and stars, which SwiftUI draws behind the island.
@MainActor @Observable
final class SkyState {
    var top = Color(red: 0.42, green: 0.74, blue: 0.98)
    var bottom = Color(red: 0.86, green: 0.95, blue: 1.0)
    var stars: Double = 0
    var twinkle: Double = 0
}

struct IslandSkyBackdrop: View {
    let state: SkyState

    var body: some View {
        ZStack {
            LinearGradient(colors: [state.top, state.bottom], startPoint: .top, endPoint: .bottom)
            if state.stars > 0.01 {
                IslandStars(twinkle: state.twinkle).opacity(state.stars)
            }
        }
    }
}

private struct IslandStars: View {
    let twinkle: Double

    var body: some View {
        Canvas { context, size in
            // The same sky every time: a fixed little generator.
            var seed: UInt64 = 0x5EED
            func next() -> Double {
                seed = seed &* 6364136223846793005 &+ 1442695040888963407
                return Double(seed >> 33) / Double(1 << 31)
            }
            for i in 0..<80 {
                let x = next() * size.width
                let y = next() * size.height * 0.5
                let r = 0.5 + next() * 1.1
                let glint = 0.55 + 0.45 * sin(twinkle * (1.5 + next() * 2) + Double(i))
                context.fill(Path(ellipseIn: CGRect(x: x - r, y: y - r, width: r * 2, height: r * 2)),
                             with: .color(.white.opacity(glint)))
            }
        }
        .allowsHitTesting(false)
    }
}

/// The sky over the island: a day that goes round in two minutes, the sun
/// and moon crossing it, the light they cast, clouds, shooting stars at
/// night, and a boat now and then.
///
/// Secrets: click the sun five times and it puts on sunglasses; click the
/// moon and it turns into the app's icon (some nights it already is); click
/// a cloud and it rains; click a shooting star for fireworks; click the boat
/// and it toots.
@MainActor
final class IslandSky {
    let state = SkyState()
    let root = Entity()
    let light = DirectionalLight()
    let fill = DirectionalLight()
    var reduceMotion = false

    /// One day, in seconds.
    let dayLength: Float = 120
    private(set) var hour: Float
    /// 1 by day, 0 by night, in between at dawn and dusk.
    private(set) var daylight: Float = 1
    private(set) var sunHeight: Float = 1

    private let effects: IslandEffects
    private let icon: TextureResource?
    private var t: Float = 0
    private var lastSky: Float = -1

    private let sun = Entity()
    private let sunCore = Island.disc(radius: 3, Island.unlit(1, 0.86, 0.34), z: 0)
    private let sunRays = Entity()
    private var sunTone: Float = -1
    private let glasses = Entity()
    private var sunTaps: [Float] = []
    private var sunBoop: Float = 0

    private let moon = Entity()
    private let moonPlain = Entity()
    private var moonIcon: Entity?
    private var moonSpin: Float = 0
    private var moonSwapped = false
    private var nights = 0

    private var clouds: [(entity: Entity, speed: Float)] = []
    private var shooting: [(entity: Entity, age: Float, velocity: SIMD3<Float>)] = []
    private var nextShooting: Float = 3

    private let boat = Entity()
    private var boatX: Float?
    private var nextBoat: Float = 25
    private var boatLift: Float = 0
    private var boatLiftVelocity: Float = 0

    /// Where the camera usually is: the sky's things are placed around it,
    /// beyond the sea's edge, so the sun and moon set behind the horizon.
    var eye = SIMD3<Float>(0, 1.48, 5.1)

    init(effects: IslandEffects, icon: TextureResource?) {
        self.effects = effects
        self.icon = icon
        hour = 12
    }

    func build(into world: Entity, seaY: Float, startHour: Float) {
        hour = startHour
        world.addChild(root)

        light.light.intensity = 2600
        light.shadow = DirectionalLightComponent.Shadow(maximumDistance: 8, depthBias: 2)
        world.addChild(light)
        fill.light.intensity = 700
        fill.light.color = Island.color(0.75, 0.85, 1)
        fill.look(at: .zero, from: [-3, 1.5, -2], relativeTo: nil)
        world.addChild(fill)

        // The sun: a flat cartoon sun facing the camera, a bright disc in
        // two soft glows, with a ring of rounded rays turning slowly.
        sun.name = "sun"
        sun.components.set(BillboardComponent())
        sun.addChild(Island.disc(radius: 5.4, Island.unlit(1, 0.93, 0.6, opacity: 0.16), z: -0.3))
        sun.addChild(Island.disc(radius: 4.0, Island.unlit(1, 0.92, 0.55, opacity: 0.35), z: -0.2))
        for k in 0..<12 {
            let ray = ModelEntity(mesh: .generateBox(width: 0.55, height: 1.2, depth: 0.01, cornerRadius: 0.27),
                                  materials: [Island.unlit(1, 0.80, 0.30)])
            let a = Float(k) / 12 * 2 * .pi
            ray.position = [sin(a) * 4.3, cos(a) * 4.3, -0.1]
            ray.orientation = simd_quatf(angle: -a, axis: [0, 0, 1])
            sunRays.addChild(ray)
        }
        sun.addChild(sunRays)
        sun.addChild(sunCore)
        sun.components.set(CollisionComponent(shapes: [.generateSphere(radius: 5)], mode: .trigger))
        sun.components.set(InputTargetComponent())
        for side: Float in [-1, 1] {
            let lens = ModelEntity(mesh: .generateSphere(radius: 1), materials: [Island.unlit(0.08, 0.08, 0.12)])
            lens.scale = [1.15, 0.8, 0.05]
            lens.position = [side * 1.2, 0.45, 0.2]
            glasses.addChild(lens)
            let glint = ModelEntity(mesh: .generateSphere(radius: 0.22), materials: [Island.unlit(1, 1, 1, opacity: 0.8)])
            glint.scale = [1, 1, 0.05]
            glint.position = [side * 1.2 - 0.35, 0.75, 0.3]
            glasses.addChild(glint)
        }
        let bridge = ModelEntity(mesh: .generateBox(width: 0.9, height: 0.18, depth: 0.02), materials: [Island.unlit(0.08, 0.08, 0.12)])
        bridge.position = [0, 0.7, 0.2]
        glasses.addChild(bridge)
        glasses.isEnabled = false
        sun.addChild(glasses)
        root.addChild(sun)

        // The moon, with a couple of craters; or, some nights, the icon.
        moon.name = "moon"
        moon.components.set(BillboardComponent())
        moonPlain.addChild(Island.disc(radius: 3.2, Island.unlit(0.85, 0.92, 1, opacity: 0.15), z: -0.2))
        moonPlain.addChild(Island.disc(radius: 2.2, Island.unlit(0.97, 0.96, 0.88), z: 0))
        for (x, y, r) in [(Float(0.7), Float(0.6), Float(0.5)), (-0.8, -0.4, 0.35), (0.2, -0.9, 0.25)] {
            moonPlain.addChild(Island.disc(radius: r, Island.unlit(0.87, 0.86, 0.78), z: 0.05, at: [x, y]))
        }
        moon.addChild(moonPlain)
        if let icon {
            let card = Island.card(icon, size: 5.4)
            card.isEnabled = false
            moon.addChild(card)
            moonIcon = card
        }
        moon.components.set(CollisionComponent(shapes: [.generateSphere(radius: 2.8)], mode: .trigger))
        moon.components.set(InputTargetComponent())
        root.addChild(moon)

        // Clouds: lumps of white drifting across, far off.
        for i in 0..<3 {
            let cloud = Entity()
            cloud.name = "cloud"
            for k in 0..<5 {
                let puff = ModelEntity(mesh: .generateSphere(radius: 1), materials: [Island.material(1, 1, 1)])
                let x = Float(k) - 2
                puff.position = [x * 0.9, -abs(x) * 0.15 + (k % 2 == 0 ? 0.25 : 0), Float.random(in: -0.2...0.2)]
                puff.scale = SIMD3(repeating: 1.15 - abs(x) * 0.18)
                cloud.addChild(puff)
            }
            cloud.position = [Float(i) * 22 - 25 + Float.random(in: -4...4), Float.random(in: 6...9), Float.random(in: -46 ... -38)]
            cloud.scale = SIMD3(repeating: Float.random(in: 1.3...1.9))
            cloud.components.set(CollisionComponent(shapes: [.generateBox(width: 6, height: 2.5, depth: 2.5)], mode: .trigger))
            cloud.components.set(InputTargetComponent())
            root.addChild(cloud)
            clouds.append((cloud, Float.random(in: 0.35...0.6)))
        }

        // The boat, with the icon for a sail; off until it next crosses.
        boat.name = "boat"
        let hull = ModelEntity(mesh: .generateBox(width: 1.4, height: 0.32, depth: 0.5, cornerRadius: 0.12), materials: [Island.material(0.78, 0.30, 0.26)])
        boat.addChild(hull)
        let deck = ModelEntity(mesh: .generateBox(width: 1.3, height: 0.06, depth: 0.42, cornerRadius: 0.02), materials: [Island.material(0.95, 0.92, 0.85)])
        deck.position.y = 0.17
        boat.addChild(deck)
        let mast = ModelEntity(mesh: .generateCylinder(height: 1.6, radius: 0.04), materials: [Island.material(0.55, 0.38, 0.22)])
        mast.position = [0, 0.95, 0]
        boat.addChild(mast)
        let sail: Entity = icon.map { Island.card($0, size: 1.0) }
            ?? ModelEntity(mesh: .generatePlane(width: 0.9, height: 1.0), materials: [Island.material(1, 1, 1)])
        sail.position = [0.04, 1.05, 0.06]
        boat.addChild(sail)
        let flag = ModelEntity(mesh: .generateBox(width: 0.25, height: 0.14, depth: 0.01), materials: [Island.material(0.95, 0.8, 0.25)])
        flag.position = [0.14, 1.72, 0]
        boat.addChild(flag)
        boat.components.set(CollisionComponent(shapes: [ShapeResource.generateBox(width: 1.6, height: 2.1, depth: 0.7).offsetBy(translation: [0, 0.8, 0])], mode: .trigger))
        boat.components.set(InputTargetComponent())
        boat.isEnabled = false
        root.addChild(boat)

        update(0, seaY: seaY)
    }

    private func skyPoint(azimuth: Float, elevation: Float, distance: Float) -> SIMD3<Float> {
        eye + distance * SIMD3(cos(elevation) * sin(azimuth), sin(elevation), -cos(elevation) * cos(azimuth))
    }

    // MARK: Taps

    func tapped(_ entity: Entity) -> Bool {
        switch entity.name {
        case "sun":
            sunBoop = 1
            sunTaps = sunTaps.filter { t - $0 < 3 } + [t]
            effects.burst(in: root, at: sun.position, colors: [Island.color(1, 0.95, 0.5), Island.color(1, 0.7, 0.3)],
                          count: 40, speed: 6, size: 0.35, life: 0.9, gravity: 0, spread: .pi)
            if sunTaps.count >= 5 && !glasses.isEnabled {
                glasses.isEnabled = true
                effects.burst(in: root, at: sun.position, colors: [Island.color(1, 1, 1), Island.color(1, 0.9, 0.4)],
                              count: 120, speed: 10, size: 0.4, life: 1.2, gravity: 0, spread: .pi)
            }
        case "moon":
            moonSpin = 1
            moonSwapped = false
        case "cloud":
            effects.rain(from: entity, at: [0, -1, 0], seconds: 3)
        case "star":
            firework(at: entity.position)
            shooting.removeAll { $0.entity === entity }
            entity.removeFromParent()
        case "boat":
            boatLiftVelocity = 2.4
            effects.burst(in: boat, at: [0, 1.85, 0], colors: [Island.color(1, 1, 1), Island.color(0.85, 0.85, 0.9)],
                          count: 30, speed: 0.9, size: 0.2, life: 1.8, gravity: -0.6, spread: 0.5)
        default:
            return false
        }
        return true
    }

    private func firework(at p: SIMD3<Float>) {
        let palettes: [[IslandColor]] = [
            [Island.color(1, 0.4, 0.4), Island.color(1, 0.85, 0.3)],
            [Island.color(0.4, 0.8, 1), Island.color(0.8, 0.5, 1)],
            [Island.color(0.5, 1, 0.6), Island.color(1, 1, 1)],
        ]
        for (i, colors) in palettes.shuffled().prefix(2).enumerated() {
            effects.burst(in: root, at: p + [Float(i) * 1.5, Float(i), 0], colors: colors, count: 140,
                          speed: 7, size: 0.28, life: 1.5, gravity: 2, spread: .pi)
        }
        // And the icon, briefly, where the star was: wish granted.
        if let icon {
            let card = Island.card(icon, size: 1)
            card.components.set(BillboardComponent())
            effects.float(card, in: root, at: p, life: 2.2, from: SIMD3(repeating: 0.5), to: SIMD3(repeating: 4.5), fadeAt: 0.4)
        }
    }

    // MARK: Each frame

    func update(_ dt: Float, seaY: Float) {
        t += dt
        let before = hour
        hour = (hour + dt / dayLength * 24).truncatingRemainder(dividingBy: 24)
        if before < 18 && hour >= 18 { nights += 1 }

        // The sun's arc: up at six, highest at noon, down at six.
        let a = 2 * Float.pi * (hour - 6) / 24
        let s = sin(a)
        sunHeight = s
        daylight = Island.smoothstep(-0.15, 0.2, s)
        let night = 1 - daylight

        sun.position = skyPoint(azimuth: -cos(a) * 0.75, elevation: 0.145 * s, distance: 85)
        moon.position = skyPoint(azimuth: cos(a) * 0.75, elevation: -0.15 * s, distance: 85)
        if s < -0.3 { glasses.isEnabled = false; sunTaps = [] }

        sunBoop = max(0, sunBoop - dt * 2)
        sun.scale = SIMD3(repeating: 1 + 0.18 * sunBoop * sin(sunBoop * 18))
        if !reduceMotion { sunRays.orientation = simd_quatf(angle: -t * 0.15, axis: [0, 0, 1]) }
        // Orange low in the sky, yellow high up.
        let tone = (Island.smoothstep(0, 0.4, s) * 20).rounded() / 20
        if tone != sunTone {
            sunTone = tone
            let c = Island.mix(SIMD3<Float>(1, 0.55, 0.25), [1, 0.86, 0.34], tone)
            sunCore.model?.materials = [Island.unlit(Double(c.x), Double(c.y), Double(c.z))]
        }

        // The moon is the icon one night in three, or when it's clicked.
        if hour > 17.5 && hour < 18 { moonSwapped = false; moonSpin = 0; setIconMoon(nights % 3 == 2) }
        if moonSpin > 0 {
            let was = moonSpin
            moonSpin = max(0, moonSpin - dt / 0.9)
            if was > 0.5 && moonSpin <= 0.5 && !moonSwapped {
                moonSwapped = true
                setIconMoon(!(moonIcon?.isEnabled ?? false))
            }
            moonPlain.orientation = simd_quatf(angle: (1 - moonSpin) * 2 * .pi, axis: [0, 1, 0])
            moonIcon?.orientation = moonPlain.orientation
        }

        // The light: the sun's, or by night the moon's, low and long at either end.
        let up = daylight >= 0.5
        let arc = up ? a : a + .pi
        let height = up ? s : -s
        light.look(at: .zero, from: [-cos(arc) * 6, 1 + 7 * max(height, 0.08), 4], relativeTo: nil)
        let warm = Island.mix(SIMD3<Float>(1, 0.6, 0.38), [1, 0.97, 0.9], Island.smoothstep(0, 0.45, s))
        let lightColor = up ? warm : SIMD3<Float>(0.62, 0.72, 1)
        light.light.color = Island.color(Double(lightColor.x), Double(lightColor.y), Double(lightColor.z))
        light.light.intensity = 2800 * daylight + 500 * night
        fill.light.intensity = 700 * daylight + 160 * night

        // Clouds drift by and come round again.
        for c in clouds {
            c.entity.position.x += c.speed * dt
            if c.entity.position.x > 42 { c.entity.position.x = -42 }
        }

        // Shooting stars, on dark nights.
        if night > 0.8 && !reduceMotion {
            nextShooting -= dt
            if nextShooting <= 0 {
                nextShooting = Float.random(in: 4...10)
                launchShootingStar()
            }
        }
        shooting = shooting.compactMap { s in
            var s = s
            s.age += dt
            s.entity.position += s.velocity * dt
            s.entity.scale = SIMD3(repeating: max(0.01, 1 - s.age / 1.3))
            if s.age > 1.3 { s.entity.removeFromParent(); return nil }
            return s
        }

        // A boat now and then.
        nextBoat -= dt
        if boatX == nil && nextBoat <= 0 {
            boatX = -11
            boat.isEnabled = true
        }
        if var x = boatX {
            x += 0.9 * dt
            boatX = x
            boatLiftVelocity -= 6 * dt
            boatLift = max(0, boatLift + boatLiftVelocity * dt)
            if boatLift == 0 { boatLiftVelocity = max(0, boatLiftVelocity) }
            boat.position = [x, seaY + 0.06 + 0.05 * sin(t * 2) + boatLift, -6.5]
            boat.orientation = simd_quatf(angle: 0.06 * sin(t * 1.7), axis: [1, 0, 0]) * simd_quatf(angle: boatLift * 0.4, axis: [0, 0, 1])
            if x > 11 {
                boatX = nil
                boat.isEnabled = false
                nextBoat = Float.random(in: 60...90)
            }
        }

        // The sky's colours and stars, a few times a second.
        if t - lastSky > 1 / 15 || lastSky < 0 {
            lastSky = t
            let (top, bottom) = Self.skyColours(at: hour)
            state.top = Color(red: Double(top.x), green: Double(top.y), blue: Double(top.z))
            state.bottom = Color(red: Double(bottom.x), green: Double(bottom.y), blue: Double(bottom.z))
            state.stars = Double(Island.smoothstep(0.3, 0.9, night))
            state.twinkle = Double(t)
        }
    }

    /// Ambient light for the lit materials: dimmer and bluer by night,
    /// warmer at dawn and dusk.
    var ambient: (intensity: Float, color: SIMD3<Float>) {
        let dusk = Island.mix(SIMD3<Float>(1, 0.78, 0.62), [1, 1, 1], Island.smoothstep(0.1, 0.5, sunHeight))
        return (Island.mix(0.1, 0.28, daylight), Island.mix([0.55, 0.62, 1], dusk, daylight))
    }

    func debugBoatSoon() { nextBoat = min(nextBoat, 0.5) }

    private func setIconMoon(_ on: Bool) {
        guard let moonIcon else { return }
        moonIcon.isEnabled = on
        moonPlain.isEnabled = !on
    }

    private func launchShootingStar() {
        let start = skyPoint(azimuth: Float.random(in: -0.6...0.6), elevation: Float.random(in: 0.12...0.2), distance: 85)
        let direction = normalize(SIMD3<Float>(Bool.random() ? 1 : -1, -0.45, 0))
        let star = ModelEntity(mesh: .generateSphere(radius: 0.35), materials: [Island.unlit(1, 0.97, 0.8)])
        star.name = "star"
        star.position = start
        star.orientation = simd_quatf(from: [1, 0, 0], to: direction)
        star.addChild({
            let tail = ModelEntity(mesh: .generateSphere(radius: 0.35), materials: [Island.unlit(1, 0.95, 0.75, opacity: 0.45)])
            tail.scale = [7, 0.55, 0.55]
            tail.position = [-2.2, 0, 0]
            return tail
        }())
        star.components.set(CollisionComponent(shapes: [.generateSphere(radius: 3)], mode: .trigger))
        star.components.set(InputTargetComponent())
        root.addChild(star)
        shooting.append((star, 0, direction * 24))
    }

    /// The sky's top and bottom colours through the day.
    private static func skyColours(at hour: Float) -> (SIMD3<Float>, SIMD3<Float>) {
        let night = (SIMD3<Float>(0.03, 0.04, 0.12), SIMD3<Float>(0.10, 0.12, 0.28))
        let late = (SIMD3<Float>(0.08, 0.08, 0.22), SIMD3<Float>(0.32, 0.22, 0.42))
        let dawn = (SIMD3<Float>(0.35, 0.45, 0.78), SIMD3<Float>(0.99, 0.66, 0.50))
        let day = (SIMD3<Float>(0.42, 0.74, 0.98), SIMD3<Float>(0.86, 0.95, 1.0))
        let dusk = (SIMD3<Float>(0.30, 0.30, 0.62), SIMD3<Float>(0.99, 0.55, 0.35))
        let keys: [(Float, (SIMD3<Float>, SIMD3<Float>))] = [
            (0, night), (4.5, night), (5.5, late), (6.5, dawn), (8.5, day),
            (16.5, day), (18, dusk), (19, late), (20.5, night), (24, night),
        ]
        for i in 0..<(keys.count - 1) where hour >= keys[i].0 && hour <= keys[i + 1].0 {
            let p = (hour - keys[i].0) / max(0.001, keys[i + 1].0 - keys[i].0)
            return (Island.mix(keys[i].1.0, keys[i + 1].1.0, p), Island.mix(keys[i].1.1, keys[i + 1].1.1, p))
        }
        return night
    }
}
