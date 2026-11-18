// The terminal: libghostty-vt (built for Android by scripts/android-vt.sh, in
// the cache shared by every checkout) through JNI, and a GLES 3 renderer.
plugins {
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.android)
    alias(libs.plugins.kotlin.compose)
}

val ghosttyRev: String = rootProject.file("../third_party/ghostty/VERSION").readLines().first { !it.startsWith("#") }.trim()
val ghosttyCache: String = System.getenv("HMK_GHOSTTY_CACHE") ?: "${System.getProperty("user.home")}/Library/Caches/hammock/ghostty"

android {
    namespace = "com.homegrownsw.hammock.terminal"
    compileSdk = 35
    ndkVersion = "27.1.12297006"

    defaultConfig {
        minSdk = 33
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        ndk { abiFilters += listOf("arm64-v8a", "x86_64") }
        externalNativeBuild {
            cmake {
                arguments += listOf("-DGHOSTTY_VT_DIR=$ghosttyCache/$ghosttyRev", "-DANDROID_STL=c++_static")
            }
        }
    }

    externalNativeBuild {
        cmake { path = file("src/main/cpp/CMakeLists.txt") }
    }

    buildFeatures { compose = true }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

kotlin { jvmToolchain(17) }

dependencies {
    api(project(":client"))
    implementation(libs.core.ktx)
    implementation(platform(libs.compose.bom))
    implementation(libs.compose.ui)
    implementation(libs.compose.foundation)
    implementation(libs.compose.material3)
    implementation(libs.compose.material.icons.extended)
    implementation(libs.lifecycle.runtime.compose)
    testImplementation(libs.junit)
    androidTestImplementation(libs.junit)
    androidTestImplementation(libs.activity.compose)
    androidTestImplementation("androidx.test:core:1.6.1")
    androidTestImplementation("androidx.test:runner:1.6.2")
    androidTestImplementation("androidx.test.ext:junit:1.2.1")
}
