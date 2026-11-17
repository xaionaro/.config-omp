package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// nativeLogObservation carries exact caller-visible log identity and bytes.
//
// Example: absent and present-empty logs remain distinguishable.
type nativeLogObservation struct {
	Present     bool   `json:"present"`
	Device      string `json:"device"`
	Inode       string `json:"inode"`
	BytesBase64 string `json:"bytes_base64"`
}

// makeNativeLogObservation encodes an already captured, locked observation.
//
// Example: binary bytes remain lossless in standard base64.
func makeNativeLogObservation(
	info os.FileInfo,
	data []byte,
) (nativeLogObservation, error) {
	if info == nil {
		if len(data) != 0 {
			return nativeLogObservation{}, fmt.Errorf("absent log has content")
		}
		return nativeLogObservation{}, nil
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() {
		return nativeLogObservation{}, fmt.Errorf("log identity is unavailable or not regular")
	}
	return nativeLogObservation{
		Present:     true,
		Device:      strconv.FormatUint(uint64(stat.Dev), 10),
		Inode:       strconv.FormatUint(uint64(stat.Ino), 10),
		BytesBase64: base64.StdEncoding.EncodeToString(data),
	}, nil
}
