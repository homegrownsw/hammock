// The client side of the daemon's protocol: the connection (WebSocket over TLS
// pinned to the computer's certificate), signing in, and the model the UI
// shows. Plain Kotlin/JVM, so its tests run on any machine.
plugins {
    alias(libs.plugins.kotlin.jvm)
}

kotlin { jvmToolchain(17) }

dependencies {
    api(project(":proto"))
    api(libs.coroutines.core)
    implementation(libs.okhttp)
    testImplementation(libs.junit)
    testImplementation(libs.kotlin.test)
    testImplementation(libs.org.json)
    testImplementation(libs.coroutines.test)
    testImplementation(libs.okhttp.mockwebserver)
}
