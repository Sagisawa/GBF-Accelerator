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
}

dependencies {
    compileOnly(files("libs/libxposed-annotations.jar"))
    compileOnly("io.github.libxposed:api:101.0.1")
    implementation("androidx.annotation:annotation:1.8.0")
    implementation("androidx.webkit:webkit:1.12.0")
}
