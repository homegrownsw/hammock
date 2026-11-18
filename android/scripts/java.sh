# Sourced by the android recipes: a JDK 17 or later for Gradle. JAVA_HOME when
# it runs and is new enough, else Android Studio's own.
# shellcheck shell=bash
java_ok() { "$1/bin/java" -version 2>&1 | awk -F'"' '/version/ { split($2, v, "."); exit !(v[1] + 0 >= 17) }'; }
if [ -z "${JAVA_HOME:-}" ] || ! java_ok "$JAVA_HOME"; then
  for j in "/Applications/Android Studio.app/Contents/jbr/Contents/Home" "$(/usr/libexec/java_home -v 17+ 2>/dev/null || true)"; do
    if [ -n "$j" ] && [ -x "$j/bin/java" ] && java_ok "$j"; then export JAVA_HOME="$j"; break; fi
  done
fi
java_ok "${JAVA_HOME:-/nonexistent}" || { echo "Gradle needs a JDK 17 or later: set JAVA_HOME, or install Android Studio." >&2; exit 1; }
