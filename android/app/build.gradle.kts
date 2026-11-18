import java.util.Properties

plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.android)
    alias(libs.plugins.kotlin.compose)
}

// The version: HAMMOCK_ANDROID_VERSION in apple/iOS/Config/Version.xcconfig,
// beside the Mac's and the iPhone's, on the same line of releases. The build number counts commits, as the
// Apple apps' do (scripts/history-commits adds the history before this repo).
val versionFile = rootProject.file("../apple/iOS/Config/Version.xcconfig")
val appVersion: String = versionFile.readLines()
    .firstOrNull { it.startsWith("HAMMOCK_ANDROID_VERSION") }
    ?.substringAfter('=')?.trim() ?: "0.0.0"
val buildNumber: Int = runCatching {
    val p = ProcessBuilder("git", "rev-list", "--count", "HEAD").directory(rootProject.projectDir).start()
    p.inputStream.bufferedReader().readText().trim().toInt() + 831
}.getOrDefault(1)

// Release signing: android/keystore.properties (git-ignored), naming the upload
// key's keystore and passwords (android/README.md). Without it a release build
// is unsigned.
val keystoreProps = rootProject.file("keystore.properties").takeIf { it.exists() }?.let { f ->
    Properties().apply { f.inputStream().use { load(it) } }
}

android {
    namespace = "com.homegrownsw.hammock"
    compileSdk = 35

    defaultConfig {
        applicationId = "com.homegrownsw.hammock"
        minSdk = 33
        targetSdk = 35
        versionCode = buildNumber
        versionName = appVersion
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        // The push relay this build registers with (relay/push).
        buildConfigField("String", "PUSH_RELAY", "\"${System.getenv("HMK_PUSH_RELAY") ?: "https://push.homegrownsw.com"}\"")
        // Firebase Cloud Messaging (android/README.md): none, no notifications while the app is closed.
        for (k in listOf("FCM_PROJECT_ID", "FCM_APP_ID", "FCM_API_KEY", "FCM_SENDER_ID")) {
            buildConfigField("String", k, "\"${System.getenv("HMK_$k") ?: (project.findProperty("hammock.$k") as String? ?: "")}\"")
        }
    }

    signingConfigs {
        if (keystoreProps != null) create("release") {
            storeFile = rootProject.file(keystoreProps.getProperty("storeFile"))
            storePassword = keystoreProps.getProperty("storePassword")
            keyAlias = keystoreProps.getProperty("keyAlias")
            keyPassword = keystoreProps.getProperty("keyPassword")
        }
    }

    buildTypes {
        debug {
            // Beside a release build on the same phone, as β is beside Hammock on a Mac.
            applicationIdSuffix = ".dev"
            versionNameSuffix = "-dev"
        }
        release {
            if (keystoreProps != null) signingConfig = signingConfigs.getByName("release")
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    buildFeatures {
        compose = true
        buildConfig = true
    }
    packaging {
        resources.excludes += listOf("META-INF/*.kotlin_module", "META-INF/versions/**")
    }
}

kotlin { jvmToolchain(17) }

dependencies {
    implementation(project(":client"))
    implementation(project(":terminal"))
    implementation(project(":agentview"))
    implementation(project(":panes"))
    implementation(project(":media"))
    implementation(platform(libs.compose.bom))
    implementation(libs.compose.ui)
    implementation(libs.compose.foundation)
    implementation(libs.compose.material3)
    implementation(libs.compose.material3.adaptive.navigation.suite)
    implementation(libs.compose.material.icons.extended)
    implementation(libs.compose.ui.tooling.preview)
    debugImplementation(libs.compose.ui.tooling)
    implementation(libs.activity.compose)
    implementation(libs.lifecycle.runtime.compose)
    implementation(libs.lifecycle.viewmodel.compose)
    implementation(libs.lifecycle.process)
    implementation(libs.navigation.compose)
    implementation(libs.core.ktx)
    implementation(libs.coroutines.android)
    implementation(libs.datastore.preferences)
    implementation(libs.camerax.camera2)
    implementation(libs.camerax.lifecycle)
    implementation(libs.camerax.view)
    implementation(libs.mlkit.barcode)
    implementation(libs.biometric)
    implementation(libs.fragment.ktx)
    implementation(libs.firebase.messaging)
    testImplementation(libs.junit)
    androidTestImplementation("androidx.test:runner:1.6.2")
    androidTestImplementation("androidx.test:rules:1.6.1")
    androidTestImplementation("androidx.test.ext:junit:1.2.1")
}
