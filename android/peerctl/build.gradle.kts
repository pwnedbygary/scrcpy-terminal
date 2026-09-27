import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    alias(libs.plugins.kotlin.jvm)
    application
}

java {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
}

kotlin {
    compilerOptions {
        jvmTarget = JvmTarget.JVM_17
    }
}

application {
    mainClass = "io.github.pwnedbygary.scterm.peerctl.MainKt"
}

dependencies {
    implementation(project(":peer"))
    implementation(libs.jcodec)
    implementation(libs.jcodec.javase)
}
