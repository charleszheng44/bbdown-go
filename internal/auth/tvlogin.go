package auth

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"math/rand"
	"net/url"
	"strings"
	"time"
)

// TV-login constants. These match the values BBDown uses; Bilibili's
// passport-tv endpoints validate both the sign HMAC and several of
// these fields, so do not change any of them casually.
const (
	tvAppkey   = "4409e2ce8ffd12b8"
	tvAppsec   = "59b43e04ad6965f34319062b478f83dd"
	tvMobiApp  = "android_tv_yst"
	tvPlatform = "android"
	tvBuild    = "102801"
	tvChannel  = "master"
	tvDevice   = "OnePlus"
	tvDeviceNm = "OnePlus7TPro"
	tvPlat     = "Android10OnePlusHD1910"
	tvSysVer   = "29"
	tvNetwork  = "wifi"
	tvIDLen    = 20
	tvBuvidLen = 37
	tvFPSuffix = 45
)

// tvSign returns the lowercase hex md5 of (queryString + appsec), which
// Bilibili validates server-side.
func tvSign(queryString string) string {
	sum := md5.Sum([]byte(queryString + tvAppsec))
	return hex.EncodeToString(sum[:])
}

// encodeOrdered builds "k1=v1&k2=v2..." preserving the caller's slice
// order, URL-escaping values. Bilibili's sign check is order-sensitive;
// callers must pass params in the exact order BBDown uses.
func encodeOrdered(kvs [][2]string) string {
	var b strings.Builder
	for i, kv := range kvs {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(kv[0]))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(kv[1]))
	}
	return b.String()
}

// tvLoginParams builds the 20-field parameter list for both the
// auth_code and poll endpoints. Pass "" for authCode on the initial
// auth_code request. nowFn/randStr are injected for deterministic
// testing; production callers pass timeNowUnix and randomAlnum.
func tvLoginParams(authCode string, nowFn func() int64, randStr func(n int) string) [][2]string {
	deviceID := randStr(tvIDLen)
	buvid := randStr(tvBuvidLen)
	ts := fmt.Sprintf("%d", nowFn())
	fingerprint := time.Unix(nowFn(), 0).UTC().Format("20060102150405000") + randStr(tvFPSuffix)

	params := [][2]string{
		{"appkey", tvAppkey},
		{"auth_code", authCode},
		{"bili_local_id", deviceID},
		{"build", tvBuild},
		{"buvid", buvid},
		{"channel", tvChannel},
		{"device", tvDevice},
		{"device_id", deviceID},
		{"device_name", tvDeviceNm},
		{"device_platform", tvPlat},
		{"fingerprint", fingerprint},
		{"guid", buvid},
		{"local_fingerprint", fingerprint},
		{"local_id", buvid},
		{"mobi_app", tvMobiApp},
		{"networkstate", tvNetwork},
		{"platform", tvPlatform},
		{"sys_ver", tvSysVer},
		{"ts", ts},
	}
	sig := tvSign(encodeOrdered(params))
	return append(params, [2]string{"sign", sig})
}

// timeNowUnix / randomAlnum are the production injections for
// tvLoginParams. Exposed so tests stay deterministic.
func timeNowUnix() int64 { return time.Now().Unix() }

var tvRandAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz_0123456789"

func randomAlnum(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = tvRandAlphabet[rand.Intn(len(tvRandAlphabet))]
	}
	return string(b)
}
