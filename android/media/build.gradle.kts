// Video-backed panes: browser and simulator panes, and whole screens, as live
// H.264 from the daemon (over QUIC, or in band on the connection), decoded by
// MediaCodec. The QUIC is the daemon's own (quic-go), as libhmkquic.so, built
// from cmd/hmkquic by scripts/android-quic.sh (Go and the NDK).
plugins {
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.android)
    alias(libs.plugins.kotlin.compose)
}

android {
    namespace = "com.homegrownsw.hammock.media"
    compileSdk = 35
    defaultConfig { minSdk = 33 }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    buildFeatures { compose = true }
    // The stream's tests run on the JVM: android.* calls they reach (logging)
    // return defaults rather than throw.
    testOptions { unitTests.isReturnDefaultValues = true }
    sourceSets["main"].jniLibs.srcDir(layout.buildDirectory.dir("hmkquic"))
}

val repo = rootProject.file("..")
val hmkquic = tasks.register<Exec>("hmkquic") {
    description = "Builds libhmkquic.so, the QUIC video transport, from cmd/hmkquic."
    inputs.files(repo.resolve("cmd/hmkquic"), repo.resolve("internal/mediaview"), repo.resolve("go.mod"), repo.resolve("go.sum"))
    inputs.file(repo.resolve("scripts/android-quic.sh"))
    val out = layout.buildDirectory.dir("hmkquic")
    outputs.dir(out)
    commandLine(repo.resolve("scripts/android-quic.sh").path, out.get().asFile.path)
}
tasks.named("preBuild") { dependsOn(hmkquic) }

kotlin { jvmToolchain(17) }

dependencies {
    api(project(":client"))
    implementation(platform(libs.compose.bom))
    implementation(libs.compose.ui)
    implementation(libs.compose.foundation)
    implementation(libs.compose.material3)
    implementation(libs.compose.material.icons.extended)
    implementation(libs.lifecycle.runtime.compose)
    // BackHandler for the screen viewer.
    implementation(libs.activity.compose)
    // FileProvider, for sharing a simulator's screenshot.
    implementation(libs.core.ktx)
    implementation(libs.coroutines.android)
    testImplementation(libs.junit)
    testImplementation(libs.kotlin.test)
    testImplementation(libs.coroutines.test)
}
