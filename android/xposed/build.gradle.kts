plugins {
    id("com.android.application")
}

android {
    namespace = "com.sagisawa.gbfaccelerator.xposed"
    compileSdk = 35

    defaultConfig {
        applicationId = "com.sagisawa.gbfaccelerator.xposed"
        minSdk = 26
        targetSdk = 35
        versionCode = 1
        versionName = "1.0.0"
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            signingConfig = signingConfigs.getByName("debug")
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro"
            )
        }
        debug {
            isMinifyEnabled = false
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    packaging {
        jniLibs {
            useLegacyPackaging = true
        }
    }

    testOptions {
        unitTests.isReturnDefaultValues = true
    }
}

dependencies {
    compileOnly("io.github.libxposed:api:102.0.0")
    implementation("androidx.annotation:annotation:1.8.0")
    implementation("androidx.webkit:webkit:1.12.0")
    testImplementation("junit:junit:4.13.2")
}

val buildGoCore = tasks.register<Exec>("buildGoCore") {
    val engineDir = rootProject.projectDir.parentFile.resolve("engine")
    workingDir = engineDir
    environment("GOOS", "android")
    environment("GOARCH", "arm64")
    environment("CGO_ENABLED", "0")
    commandLine("go", "build", "-trimpath", "-ldflags=-s -w", "-o", file("src/main/jniLibs/arm64-v8a/libgbfcore.so").absolutePath, ".")
}

tasks.named("preBuild") {
    dependsOn(buildGoCore)
}
