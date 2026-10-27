package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Passwords use argon2id (RFC 9106) in the PHC string format, so stored
// parameters can be raised later without breaking existing hashes.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKey     = 32
)

// hashing bounds memory: each hash briefly uses argonMemory KiB.
var hashing = make(chan struct{}, 2)

func argonKeyOf(password string, salt []byte, t, m uint32, p uint8, size uint32) []byte {
	hashing <- struct{}{}
	defer func() { <-hashing }()
	return argon2.IDKey([]byte(password), salt, t, m, p, size)
}

func hashPassword(password string) string {
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		panic(e)
	}
	key := argonKeyOf(password, salt, argonTime, argonMemory, argonThreads, argonKey)
	b64 := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads, b64(salt), b64(key))
}

func checkPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return false
	}
	var m, t uint32
	var p uint8
	if _, e := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); e != nil || m < 8*1024 || m > 1<<20 || t < 1 || t > 10 || p < 1 {
		return false
	}
	salt, e1 := base64.RawStdEncoding.DecodeString(parts[4])
	key, e2 := base64.RawStdEncoding.DecodeString(parts[5])
	if e1 != nil || e2 != nil || len(salt) < 8 || len(key) < 16 || len(key) > 64 {
		return false
	}
	return subtle.ConstantTimeCompare(argonKeyOf(password, salt, t, m, p, uint32(len(key))), key) == 1
}

// dummyHash lets unknown usernames cost as much as wrong passwords.
var dummyHash = sync.OnceValue(func() string { return hashPassword(random()) })
