plugins {
    kotlin("jvm") version "1.9.22"
    id("io.ktor.plugin") version "2.3.9"
}

kotlin {
    jvmToolchain(21)
}

dependencies {
    implementation("io.ktor:ktor-server-core:2.3.9")
    testImplementation(kotlin("test"))
}
