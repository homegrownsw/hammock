# Protobuf lite reads its messages' fields by reflection.
-keep class * extends com.google.protobuf.GeneratedMessageLite { *; }
# JNI: the terminal's native code calls back into these.
-keep class com.homegrownsw.hammock.terminal.** { native <methods>; }
-dontwarn org.bouncycastle.**
-dontwarn org.conscrypt.**
-dontwarn org.openjsse.**
