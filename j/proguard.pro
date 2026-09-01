# ============================================================
# ProGuard obfuscation rules for kb-chat-flow
# ============================================================

# --- JDK target ---
-target 17

# --- Don't shrink (risk of removing reflection-dependent code) ---
-dontshrink

# --- Don't optimize (reduce risk of bytecode bugs) ---
-dontoptimize

# --- Obfuscation ---
-useuniqueclassmembernames
-keepattributes Exceptions,InnerClasses,Signature,Deprecated,SourceFile,LineNumberTable,*Annotation*,EnclosingMethod

# Repackage all obfuscated classes into the root package (hides original package structure)
-repackageclasses ''

# --- Suppress warnings for third-party dependencies ---
-dontwarn com.fasterxml.**
-dontwarn com.alibaba.**
-dontwarn com.google.**
-dontwarn io.netty.**
-dontwarn io.milvus.**
-dontwarn io.minio.**
-dontwarn io.grpc.**
-dontwarn okhttp3.**
-dontwarn okio.**
-dontwarn org.apache.**
-dontwarn org.slf4j.**
-dontwarn org.xerial.**
-dontwarn redis.clients.**
-dontwarn org.mindrot.**
-dontwarn org.sqlite.**
-dontwarn com.codahale.**
-dontwarn org.reactivestreams.**
-dontwarn reactor.**
-dontwarn sun.misc.**
-dontwarn javax.annotation.**
-dontwarn javax.xml.**
-dontwarn javax.naming.**
-dontwarn javax.management.**
-dontwarn javax.crypto.**
-dontwarn java.lang.invoke.**
-dontwarn org.conscrypt.**
-dontwarn org.openjsse.**
-dontwarn com.sun.jna.**
-dontwarn io.netty.internal.**
-dontwarn org.jctools.**
-dontwarn com.rd.robot.model.**

# ============================================================
# Keep ALL third-party libraries (never obfuscate)
# ============================================================
-keep class com.fasterxml.** { *; }
-keep class com.alibaba.** { *; }
-keep class io.netty.** { *; }
-keep class io.milvus.** { *; }
-keep class io.minio.** { *; }
-keep class org.apache.** { *; }
-keep class org.slf4j.** { *; }
-keep class org.xerial.** { *; }
-keep class redis.clients.** { *; }
-keep class org.mindrot.** { *; }
-keep class org.sqlite.** { *; }
-keep class okhttp3.** { *; }
-keep class okio.** { *; }
-keep class com.google.** { *; }
-keep class io.grpc.** { *; }
-keep class com.codahale.** { *; }
-keep class org.reactivestreams.** { *; }
-keep class reactor.** { *; }
-keep class com.sun.jna.** { *; }
-keep class org.conscrypt.** { *; }
-keep class org.openjsse.** { *; }
-keep class org.jctools.** { *; }
-keep class javax.annotation.** { *; }

# ============================================================
# Keep Bootstrap main entry point
# ============================================================
-keep public class com.rd.robot.Bootstrap {
    public static void main(java.lang.String[]);
}

# ============================================================
# Keep ALL model classes (Jackson @JsonProperty reflection)
# These are pure data carriers — no business logic exposed
# ============================================================
-keep class com.rd.robot.model.** {
    <fields>;
    <init>(...);
    public <methods>;
    public void set*(***);
    public *** get*();
    public boolean is*();
}

# ============================================================
# Keep interfaces (implementations must match method signatures)
# ============================================================
-keep interface com.rd.robot.repository.MetaStore { *; }
-keep interface com.rd.robot.web.controller.PresenceStore { *; }
-keep interface com.rd.robot.vector.VectorStore { *; }
-keep interface com.rd.robot.knowledge.FileStore { *; }
-keep interface com.rd.robot.session.SessionStore { *; }

# ============================================================
# Keep HttpServer, Router, RouteHandler (extensively referenced)
# ============================================================
-keep class com.rd.robot.web.server.HttpServer { *; }
-keep class com.rd.robot.web.router.Router { *; }
-keep class com.rd.robot.web.router.Router$RouteMatch { *; }
-keep interface com.rd.robot.web.router.RouteHandler { *; }

# ============================================================
# Keep config classes (startup, some Jackson usage)
# ============================================================
-keep class com.rd.robot.config.AppConfig { *; }
-keep class com.rd.robot.config.RuntimeConfig { *; }

# ============================================================
# Keep logger config
# ============================================================
-keep class com.rd.robot.logger.LogConfig { *; }

# ============================================================
# Keep record classes (special JVM semantics)
# ============================================================
-keep class com.rd.robot.fasttext.FastTextPredictor$Result { *; }
-keep class com.rd.robot.engine.TemplateResolver$SysVarInfo { *; }
-keep class com.rd.robot.web.controller.FaqController$FaqMatchResult { *; }
-keep class com.rd.robot.repository.SqliteMetaStore$DefaultConfig { *; }