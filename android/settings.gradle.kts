// Hammock for Android: the phone app for a Mac (or Linux computer) running Hammock.
// See android/README.md.
pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "Hammock"

include(":proto")     // the daemon's protocol, generated from ../proto
include(":client")    // connection, sign-in and the model (plain JVM: tests run without an emulator)
include(":terminal")  // libghostty-vt through JNI, and the GLES renderer
include(":agentview") // the agent view
include(":panes")     // editor, diff and log panes
include(":media")     // browser, simulator and screen video
include(":app")
