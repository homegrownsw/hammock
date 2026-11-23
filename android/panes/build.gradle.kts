// Editor, diff and log panes. Editor and diff panes show the same CodeMirror
// pages as the Mac and the iPhone (apple/Resources/editor.html and
// diff.html) in a WebView; log panes are drawn natively. The agent view's
// page (agent.html) is served from here too, for the agentview module.
plugins {
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.android)
    alias(libs.plugins.kotlin.compose)
}

android {
    namespace = "com.homegrownsw.hammock.panes"
    compileSdk = 35
    defaultConfig { minSdk = 33 }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    buildFeatures { compose = true }
    testOptions { unitTests.isReturnDefaultValues = true }
}

kotlin { jvmToolchain(17) }

/**
 * Copies the built pages into generated assets (assets/hammock/), so the
 * one copy in apple/Resources is what every app ships: nothing duplicated
 * in git, and a rebuilt page reaches Android with the next build.
 */
abstract class CopyPages : DefaultTask() {
    @get:InputFiles abstract val pages: ConfigurableFileCollection
    @get:OutputDirectory abstract val outputDir: DirectoryProperty

    @TaskAction
    fun copy() {
        val out = outputDir.get().asFile.resolve("hammock")
        out.deleteRecursively()
        out.mkdirs()
        for (f in pages.files) {
            if (!f.exists()) throw GradleException("${f.path} is missing: build it with `just editor-js`")
            f.copyTo(out.resolve(f.name), overwrite = true)
        }
    }
}

val copyPages = tasks.register<CopyPages>("copyEditorPages") {
    pages.from(rootProject.file("../apple/Resources/editor.html"), rootProject.file("../apple/Resources/diff.html"), rootProject.file("../apple/Resources/files.html"), rootProject.file("../apple/Resources/agent.html"))
    outputDir.set(layout.buildDirectory.dir("generated/hammockPages"))
}

androidComponents {
    onVariants { v -> v.sources.assets?.addGeneratedSourceDirectory(copyPages, CopyPages::outputDir) }
}

dependencies {
    api(project(":client"))
    implementation(platform(libs.compose.bom))
    implementation(libs.compose.ui)
    implementation(libs.compose.foundation)
    implementation(libs.compose.material3)
    implementation(libs.compose.material.icons.extended)
    implementation(libs.compose.ui.tooling.preview)
    debugImplementation(libs.compose.ui.tooling)
    implementation(libs.lifecycle.runtime.compose)
    implementation(libs.core.ktx)
    implementation(libs.coroutines.android)
    // addWebMessageListener: the pages' messages, from their own origin only.
    implementation(libs.webkit)
    testImplementation(libs.junit)
    testImplementation(libs.kotlin.test)
    testImplementation(libs.org.json)
}
