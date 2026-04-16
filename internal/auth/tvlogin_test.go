package auth

import (
	"crypto/md5"
	"encoding/hex"
	"net/url"
	"strings"
	"testing"
)

func TestTVSignIsMD5OfQueryPlusAppsec(t *testing.T) {
	params := [][2]string{
		{"appkey", "4409e2ce8ffd12b8"},
		{"auth_code", ""},
		{"ts", "1700000000"},
	}
	qs := encodeOrdered(params)
	sig := tvSign(qs)
	expected := md5Hex(qs + tvAppsec)
	if sig != expected {
		t.Fatalf("tvSign mismatch: got %s want %s", sig, expected)
	}
}

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestTVLoginParamsAreOrderedAndComplete(t *testing.T) {
	params := tvLoginParams("my-auth-code", func() int64 { return 1700000000 }, func(n int) string {
		return strings.Repeat("A", n)
	})
	// The signature must be the LAST entry.
	if params[len(params)-1][0] != "sign" {
		t.Errorf("last key should be sign, got %s", params[len(params)-1][0])
	}
	// Required keys must be present.
	must := map[string]bool{
		"appkey": false, "auth_code": false, "bili_local_id": false, "build": false,
		"buvid": false, "channel": false, "device": false, "device_id": false,
		"device_name": false, "device_platform": false, "fingerprint": false,
		"guid": false, "local_fingerprint": false, "local_id": false, "mobi_app": false,
		"networkstate": false, "platform": false, "sys_ver": false, "ts": false, "sign": false,
	}
	for _, kv := range params {
		must[kv[0]] = true
	}
	for k, seen := range must {
		if !seen {
			t.Errorf("missing required param %q", k)
		}
	}
	// ts and auth_code must be what we passed.
	for _, kv := range params {
		if kv[0] == "ts" && kv[1] != "1700000000" {
			t.Errorf("ts wrong: %s", kv[1])
		}
		if kv[0] == "auth_code" && kv[1] != "my-auth-code" {
			t.Errorf("auth_code wrong: %s", kv[1])
		}
	}
	// Signature must verify against the rest of the query.
	var (
		signValue string
		others    [][2]string
	)
	for _, kv := range params {
		if kv[0] == "sign" {
			signValue = kv[1]
			continue
		}
		others = append(others, kv)
	}
	if signValue != md5Hex(encodeOrdered(others)+tvAppsec) {
		t.Errorf("sign does not verify")
	}
}

func TestEncodeOrderedEscapesSpecialChars(t *testing.T) {
	got := encodeOrdered([][2]string{{"k1", "a b"}, {"k2", "+"}})
	want := "k1=" + url.QueryEscape("a b") + "&k2=" + url.QueryEscape("+")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
