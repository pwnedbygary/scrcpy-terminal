pluginManagement {
    repositories {
        google {
            content {
                includeGroupByRegex("com\\.android.*")
                includeGroupByRegex("com\\.google.*")
                includeGroupByRegex("androidx.*")
            }
        }
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositoriesMode = RepositoriesMode.FAIL_ON_PROJECT_REPOS
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "scterm-android"

// :protocol and :peer are plain JVM modules so the wire formats, session
// logic and TLS pairing run under host unit tests; :scrcpy-server compiles the
// vendored server source unchanged so the APK carries its own helper;
// :peerctl is a desktop controller for exercising real devices.
include(":protocol", ":peer", ":scrcpy-server", ":app", ":peerctl")
