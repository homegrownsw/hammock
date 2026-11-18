//go:build android

// Command hmkquic is the Android app's QUIC video transport: internal/mediaview
// as a shared library (libhmkquic.so, built by scripts/android-quic.sh), with
// the JNI functions of com.homegrownsw.hammock.media.Quic. The app has no
// QUIC of its own, and this is the daemon's (quic-go), so the two always agree.
package main

/*
#include <jni.h>
#include <stdlib.h>

static jbyteArray newBytes(JNIEnv* env, const void* p, int n) {
	jbyteArray a = (*env)->NewByteArray(env, n);
	if (a != NULL && n > 0) (*env)->SetByteArrayRegion(env, a, 0, n, (const jbyte*)p);
	return a;
}
static const char* getStr(JNIEnv* env, jstring s) { return (*env)->GetStringUTFChars(env, s, NULL); }
static void releaseStr(JNIEnv* env, jstring s, const char* c) { (*env)->ReleaseStringUTFChars(env, s, c); }
static int byteLen(JNIEnv* env, jbyteArray a) { return (*env)->GetArrayLength(env, a); }
static void getBytes(JNIEnv* env, jbyteArray a, int n, void* out) { (*env)->GetByteArrayRegion(env, a, 0, n, (jbyte*)out); }
static void throwIO(JNIEnv* env, const char* msg) {
	jclass c = (*env)->FindClass(env, "java/io/IOException");
	if (c != NULL) (*env)->ThrowNew(env, c, msg);
}
static jstring newStr(JNIEnv* env, const char* s) { return (*env)->NewStringUTF(env, s); }
*/
import "C"

import (
	"context"
	"sync"
	"time"
	"unsafe"

	"github.com/homegrownsw/hammock/internal/mediaview"
)

var (
	mu      sync.Mutex
	viewers = map[int64]*mediaview.Viewer{}
	nextID  int64
)

func viewer(h C.jlong) *mediaview.Viewer {
	mu.Lock()
	defer mu.Unlock()
	return viewers[int64(h)]
}

func goString(env *C.JNIEnv, s C.jstring) string {
	c := C.getStr(env, s)
	defer C.releaseStr(env, s, c)
	return C.GoString(c)
}

func throw(env *C.JNIEnv, msg string) {
	c := C.CString(msg)
	defer C.free(unsafe.Pointer(c))
	C.throwIO(env, c)
}

// open connects and presents the ticket; a handle for the others. Throws
// IOException when it can't (the message says why).
//
//export Java_com_homegrownsw_hammock_media_Quic_nativeOpen
func Java_com_homegrownsw_hammock_media_Quic_nativeOpen(env *C.JNIEnv, cls C.jclass, host C.jstring, port C.jint, pin C.jstring, ticket C.jbyteArray, datagrams C.jboolean) C.jlong {
	n := int(C.byteLen(env, ticket))
	t := make([]byte, n)
	if n > 0 {
		C.getBytes(env, ticket, C.int(n), unsafe.Pointer(&t[0]))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	v, err := mediaview.Dial(ctx, goString(env, host), int(port), goString(env, pin), t, mediaview.Options{Datagrams: datagrams != 0})
	if err != nil {
		throw(env, err.Error())
		return 0
	}
	mu.Lock()
	nextID++
	id := nextID
	viewers[id] = v
	mu.Unlock()
	return C.jlong(id)
}

// next waits up to timeoutMs for a whole frame record: null when none came,
// an empty array once the video has ended.
//
//export Java_com_homegrownsw_hammock_media_Quic_nativeNext
func Java_com_homegrownsw_hammock_media_Quic_nativeNext(env *C.JNIEnv, cls C.jclass, h C.jlong, timeoutMs C.jint) C.jbyteArray {
	v := viewer(h)
	if v == nil {
		return C.newBytes(env, nil, 0)
	}
	rec, err := v.Next(time.Duration(timeoutMs) * time.Millisecond)
	if err != nil {
		return C.newBytes(env, nil, 0)
	}
	if rec == nil {
		return C.jbyteArray(0)
	}
	return C.newBytes(env, unsafe.Pointer(&rec[0]), C.int(len(rec)))
}

//export Java_com_homegrownsw_hammock_media_Quic_nativeKeyframe
func Java_com_homegrownsw_hammock_media_Quic_nativeKeyframe(env *C.JNIEnv, cls C.jclass, h C.jlong) {
	if v := viewer(h); v != nil {
		v.Keyframe()
	}
}

// reason is why the video ended, if the connection said ("" otherwise).
//
//export Java_com_homegrownsw_hammock_media_Quic_nativeReason
func Java_com_homegrownsw_hammock_media_Quic_nativeReason(env *C.JNIEnv, cls C.jclass, h C.jlong) C.jstring {
	r := ""
	if v := viewer(h); v != nil {
		r = v.Reason()
	}
	c := C.CString(r)
	defer C.free(unsafe.Pointer(c))
	return C.newStr(env, c)
}

// close ends the connection and forgets the handle.
//
//export Java_com_homegrownsw_hammock_media_Quic_nativeClose
func Java_com_homegrownsw_hammock_media_Quic_nativeClose(env *C.JNIEnv, cls C.jclass, h C.jlong) {
	mu.Lock()
	v := viewers[int64(h)]
	delete(viewers, int64(h))
	mu.Unlock()
	if v != nil {
		v.Close()
	}
}

func main() {}
