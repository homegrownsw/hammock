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

// Release signing: HMK_ANDROID_KEYSTORE, _KEYSTORE_PASSWORD and _KEY_ALIAS (what
// scripts/release passes, from 1Password), else android/keystore.properties
// (git-ignored), naming the keystore and its passwords (android/README.md).
// Without either a release build is unsigned.
val keystoreProps: Properties? = System.getenv("HMK_ANDROID_KEYSTORE")?.let { path ->
    val pw = System.getenv("HMK_ANDROID_KEYSTORE_PASSWORD") ?: throw GradleException("HMK_ANDROID_KEYSTORE_PASSWORD isn't set")
    Properties().apply {
        setProperty("storeFile", path)
        setProperty("storePassword", pw)
        setProperty("keyAlias", System.getenv("HMK_ANDROID_KEY_ALIAS") ?: "hammock")
        setProperty("keyPassword", pw)
    }
} ?: rootProject.file("keystore.properties").takeIf { it.exists() }?.let { f ->
    Properties().apply { f.inputStream().use { load(it) } }
}

// Firebase Cloud Messaging (android/README.md): each build type's values from
// the google-services.json Firebase gives for its package (git-ignored:
// app/google-services.json, app/src/debug/google-services.json), or HMK_FCM_*
// (or hammock.FCM_* Gradle properties), which win. None: no notifications
// while the app is closed.
fun fcm(type: com.android.build.api.dsl.ApplicationBuildType, file: String, pkg: String) {
    val found = HashMap<String, String>()
    project.file(file).takeIf { it.exists() }?.let { f ->
        @Suppress("UNCHECKED_CAST")
        val json = groovy.json.JsonSlurper().parse(f) as Map<String, Any?>
        val info = json["project_info"] as Map<String, Any?>
        val client = (json["client"] as List<Map<String, Any?>>).firstOrNull {
            ((it["client_info"] as Map<String, Any?>)["android_client_info"] as Map<String, Any?>)["package_name"] == pkg
        } ?: throw GradleException("$file has no app for $pkg")
        found["FCM_PROJECT_ID"] = info["project_id"] as String
        found["FCM_SENDER_ID"] = info["project_number"] as String
        found["FCM_APP_ID"] = (client["client_info"] as Map<String, Any?>)["mobilesdk_app_id"] as String
        found["FCM_API_KEY"] = ((client["api_key"] as List<Map<String, Any?>>).first())["current_key"] as String
    }
    for (k in listOf("FCM_PROJECT_ID", "FCM_APP_ID", "FCM_API_KEY", "FCM_SENDER_ID")) {
        val v = System.getenv("HMK_$k") ?: project.findProperty("hammock.$k") as String? ?: found[k] ?: ""
        type.buildConfigField("String", k, "\"$v\"")
    }
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
    }

    signingConfigs {
        if (keystoreProps != null) create("release") {
            storeFile = rootProject.file(keystoreProps.getProperty("storeFile")) // (an absolute path stays as it is)
            storePassword = keystoreProps.getProperty("storePassword")
            keyAlias = keystoreProps.getProperty("keyAlias")
            keyPassword = keystoreProps.getProperty("keyPassword")
        }
    }

    buildTypes {
        debug {
            // Beside a release build on the same phone, as β is beside Hammock on a Mac.
            applicationIdSuffix = ".dev"
            fcm(this, "src/debug/google-services.json", "com.homegrownsw.hammock.dev")
            versionNameSuffix = "-dev"
        }
        release {
            if (keystoreProps != null) signingConfig = signingConfigs.getByName("release")
            // HMK_ANDROID_FIREBASE_JSON: the release's, from 1Password (scripts/release).
            fcm(this, System.getenv("HMK_ANDROID_FIREBASE_JSON") ?: "google-services.json", "com.homegrownsw.hammock")
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

/**
 * Settings ▸ Acknowledgements: the files `just acknowledgements` makes
 * (internal/acknowledgements: every app's, and what only Android ships),
 * copied into generated assets, not into git.
 */
abstract class CopyAcknowledgements : DefaultTask() {
    @get:InputFiles abstract val sources: ConfigurableFileCollection
    @get:OutputDirectory abstract val outputDir: DirectoryProperty

    @TaskAction
    fun copy() {
        val out = outputDir.get().asFile
        out.deleteRecursively()
        out.mkdirs()
        for (f in sources.files) f.copyTo(out.resolve(f.name), overwrite = true)
    }
}

val copyAcknowledgements = tasks.register<CopyAcknowledgements>("copyAcknowledgements") {
    sources.from(rootProject.file("../internal/acknowledgements/Acknowledgements.json"),
        rootProject.file("../internal/acknowledgements/Acknowledgements-android.json"))
    outputDir.set(layout.buildDirectory.dir("generated/acknowledgements"))
}

androidComponents {
    onVariants { v -> v.sources.assets?.addGeneratedSourceDirectory(copyAcknowledgements, CopyAcknowledgements::outputDir) }
}

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
