plugins {
    alias(libs.plugins.android.application)
}

android {
    namespace = "io.github.pwnedbygary.scterm"
    compileSdk = 37

    defaultConfig {
        applicationId = "io.github.pwnedbygary.scterm"
        minSdk = 27
        targetSdk = 37
        versionCode = 1
        versionName = "0.1.0"
    }

    buildTypes {
        release {
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
        buildConfig = true
    }

    lint {
        // Lint the pure-JVM :protocol/:peer code against this minSdk as well:
        // they run on Android 8.1, so JDK 9+ library APIs are off limits.
        checkDependencies = true
        abortOnError = true
        lintConfig = file("lint.xml")
    }

    packaging {
        resources {
            excludes += "/META-INF/{AL2.0,LGPL2.1}"
        }
    }
}

dependencies {
    implementation(project(":peer"))
    implementation(project(":scrcpy-server"))
    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.activity.ktx)
    implementation(libs.androidx.annotation)
    implementation(libs.kotlinx.coroutines.android)
    testImplementation(libs.junit)
    testImplementation(libs.kotlin.test.junit)
}
