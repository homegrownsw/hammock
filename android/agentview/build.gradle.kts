// The agent view: an agent's conversation drawn natively from the daemon's
// AgentView blocks, its prompt cards and the composer.
plugins {
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.android)
    alias(libs.plugins.kotlin.compose)
}

android {
    namespace = "com.homegrownsw.hammock.agentview"
    compileSdk = 35
    defaultConfig { minSdk = 33 }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    buildFeatures { compose = true }
    // The model's tests run on the JVM: android.* calls they reach (none
    // should) return defaults rather than throw.
    testOptions { unitTests.isReturnDefaultValues = true }
}

kotlin { jvmToolchain(17) }

dependencies {
    api(project(":client"))
    // The web-drawn conversation: the page and its WebView host (PageBridge).
    implementation(project(":panes"))
    implementation(platform(libs.compose.bom))
    implementation(libs.compose.ui)
    implementation(libs.compose.foundation)
    implementation(libs.compose.material3)
    implementation(libs.compose.material.icons.extended)
    implementation(libs.compose.ui.tooling.preview)
    debugImplementation(libs.compose.ui.tooling)
    implementation(libs.lifecycle.runtime.compose)
    // BackHandler and the photo picker, document and permission launchers.
    implementation(libs.activity.compose)
    implementation(libs.core.ktx)
    implementation(libs.coroutines.android)
    testImplementation(libs.junit)
    testImplementation(libs.kotlin.test)
    testImplementation(libs.coroutines.test)
}
