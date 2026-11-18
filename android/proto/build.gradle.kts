// The daemon's protocol (../proto/hammock/v1/hammock.proto), generated as Java
// and Kotlin lite classes at build time: nothing generated is committed.
plugins {
    `java-library`
    alias(libs.plugins.kotlin.jvm)
    alias(libs.plugins.protobuf)
}

java {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
}

kotlin { jvmToolchain(17) }

sourceSets {
    main {
        proto { srcDir("../../proto") }
    }
}

dependencies {
    api(libs.protobuf.javalite)
    api(libs.protobuf.kotlin.lite)
}

protobuf {
    protoc { artifact = libs.protoc.get().toString() }
    generateProtoTasks {
        all().forEach { task ->
            task.builtins {
                named("java") { option("lite") }
                create("kotlin") { option("lite") }
            }
        }
    }
}
