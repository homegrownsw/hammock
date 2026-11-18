# libhammockterm.so: the JNI side of TerminalEngine, with libghostty-vt
# linked in statically (built for each ABI by scripts/android-vt.sh, into the
# cache every checkout shares: GHOSTTY_VT_DIR is <cache>/<commit>).
cmake_minimum_required(VERSION 3.22)
project(hammockterm LANGUAGES C CXX)

if(NOT GHOSTTY_VT_DIR)
    message(FATAL_ERROR "GHOSTTY_VT_DIR isn't set (android/terminal/build.gradle.kts passes it)")
endif()
set(VT "${GHOSTTY_VT_DIR}/android-${ANDROID_ABI}")
if(NOT EXISTS "${VT}/lib/libghostty-vt.a")
    message(FATAL_ERROR "No libghostty-vt for ${ANDROID_ABI} in ${VT}: run scripts/android-vt.sh")
endif()

add_library(ghostty-vt STATIC IMPORTED)
set_target_properties(ghostty-vt PROPERTIES
    IMPORTED_LOCATION "${VT}/lib/libghostty-vt.a"
    INTERFACE_INCLUDE_DIRECTORIES "${VT}/include")

add_library(hammockterm SHARED hammockterm.cpp)
set_target_properties(hammockterm PROPERTIES CXX_STANDARD 17 CXX_STANDARD_REQUIRED ON)
# Linked statically: its functions needn't be exported from our library.
target_compile_definitions(hammockterm PRIVATE GHOSTTY_STATIC)
target_compile_options(hammockterm PRIVATE -Wall -fvisibility=hidden)
target_link_libraries(hammockterm PRIVATE ghostty-vt log)
# Only the JNI entry points leave the library; 16 KB pages, as Android 15 wants.
target_link_options(hammockterm PRIVATE -Wl,--exclude-libs,ALL -Wl,-z,max-page-size=16384)
