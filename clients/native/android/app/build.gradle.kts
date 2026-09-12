import java.util.zip.ZipFile

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}
android {
    namespace = "ai.wuu.nativeapp"
    compileSdk = 36
    defaultConfig {
        applicationId = "ai.wuu.nativeapp"
        minSdk = 28
        targetSdk = 36
        versionCode = 260900199
        versionName = "2026.9.1"
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        for (key in listOf("APP_ID", "API_KEY", "PROJECT_ID", "SENDER_ID")) {
            val name = "WUU_FIREBASE_$key"
            val value = providers.gradleProperty(name).orElse(providers.environmentVariable(name)).getOrElse("")
            require(value.matches(Regex("[A-Za-z0-9_:-]*"))) { "$name contains invalid characters" }
            buildConfigField("String", name, "\"$value\"")
        }
    }
    buildFeatures { compose = true; buildConfig = true }
    sourceSets.getByName("main").assets.srcDir("../../licenses")
    buildTypes {
        create("uitest") {
            initWith(getByName("debug"))
            applicationIdSuffix = ".uitest"
            matchingFallbacks += listOf("debug")
        }
    }
    testBuildType = if (project.hasProperty("nativeUiTest")) "uitest" else "debug"
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }
}
dependencies {
    implementation(platform("androidx.compose:compose-bom:2025.10.00"))
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")
    implementation("androidx.activity:activity-compose:1.11.0")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.9.4")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.9.4")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.10.2")
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    implementation("com.google.firebase:firebase-messaging:25.0.1")
    implementation("com.google.android.gms:play-services-base:18.1.0")
    implementation("org.bouncycastle:bcprov-jdk18on:1.79")
    implementation("io.noties.markwon:core:4.6.2")
    implementation("io.noties.markwon:ext-strikethrough:4.6.2")
    implementation("io.noties.markwon:ext-tables:4.6.2")
    implementation("io.noties.markwon:ext-tasklist:4.6.2")
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.10.2")
    testImplementation("org.json:json:20250517")
    testImplementation("org.robolectric:robolectric:4.16.1") {
        exclude(group = "org.bouncycastle", module = "bcprov-jdk18on")
    }
    androidTestImplementation(platform("androidx.compose:compose-bom:2025.10.00"))
    androidTestImplementation("androidx.compose.ui:ui-test-junit4")
    androidTestImplementation("androidx.test:runner:1.7.0")
    androidTestImplementation("androidx.test.uiautomator:uiautomator:2.3.0")
}

// Google ships complete third-party notices inside its AARs, outside packaged resources.
// Preserve them verbatim rather than maintaining a shortened, stale license list.
val pushNoticesDirectory = layout.buildDirectory.dir("generated/push-notices")
val pushNoticeArtifacts = providers.provider { configurations.getByName("releaseRuntimeClasspath") }
val generatePushNotices = tasks.register("generatePushNotices") {
    inputs.files(pushNoticeArtifacts)
    outputs.dir(pushNoticesDirectory)
    doLast {
        val notices = linkedMapOf<String, MutableList<String>>()
        pushNoticeArtifacts.get().files.sortedBy { it.name }.filter { it.extension == "aar" }.forEach { artifact ->
            ZipFile(artifact).use { zip ->
                zip.getEntry("third_party_licenses.txt")?.let { entry ->
                    val text = zip.getInputStream(entry).bufferedReader().use { it.readText() }
                    notices.getOrPut(text) { mutableListOf() }.add(artifact.name)
                }
            }
        }
        check(notices.isNotEmpty()) { "Missing Google Play services third-party notices" }
        val output = pushNoticesDirectory.get().file("Google-Notices.txt").asFile
        output.parentFile.mkdirs()
        output.writeText(notices.entries.joinToString("\n\n") { (text, artifacts) -> artifacts.joinToString("\n") + "\n\n" + text })
    }
}
android.sourceSets.getByName("main").assets.srcDir(pushNoticesDirectory)
tasks.named("preBuild").configure { dependsOn(generatePushNotices) }
