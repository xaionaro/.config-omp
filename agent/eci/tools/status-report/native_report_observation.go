package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"syscall"
)

// nativeLogObservation names the observed file identity and exact bytes.
//
// Example: An absent log is different from a present empty log.
type nativeLogObservation struct {
	Present bool
	Device uint64
	Inode uint64
	Bytes []byte
}

// decodeNativeLogObservation rejects missing or noncanonical observation fields.
//
// Example: An absent log cannot claim a file identity.
func decodeNativeLogObservation(raw json.RawMessage) (nativeLogObservation, error) {
	value, err := decodeReportValue(raw)
	if err != nil { return nativeLogObservation{}, err }
	object, ok := value.(map[string]any)
	if !ok || len(object) != 4 { return nativeLogObservation{}, fmt.Errorf("complete log observation required") }
	present, ok := object["present"].(bool); if !ok { return nativeLogObservation{}, fmt.Errorf("invalid log presence") }
	device, ok := object["device"].(string); if !ok { return nativeLogObservation{}, fmt.Errorf("invalid log device") }
	inode, ok := object["inode"].(string); if !ok { return nativeLogObservation{}, fmt.Errorf("invalid log inode") }
	encoded, ok := object["bytes_base64"].(string); if !ok { return nativeLogObservation{}, fmt.Errorf("invalid log bytes") }
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || base64.StdEncoding.EncodeToString(data) != encoded { return nativeLogObservation{}, fmt.Errorf("invalid log bytes") }
	if !present {
		if device != "" || inode != "" || encoded != "" { return nativeLogObservation{}, fmt.Errorf("absent log has identity or bytes") }
		return nativeLogObservation{}, nil
	}
	var values [2]uint64
	for i, number := range []string{device, inode} {
		value, err := strconv.ParseUint(number, 10, 64)
		if err != nil || strconv.FormatUint(value, 10) != number { return nativeLogObservation{}, fmt.Errorf("invalid log identity") }
		values[i] = value
	}
	return nativeLogObservation{Present:true, Device:values[0], Inode:values[1], Bytes:data}, nil
}

// observeNativeLog converts the opened log identity without losing bytes.
//
// Example: An empty present file includes its device and inode.
func observeNativeLog(snapshot replayLogSnapshot) (nativeLogObservation, error) {
	if snapshot.Info == nil { return nativeLogObservation{}, nil }
	stat, ok := snapshot.Info.Sys().(*syscall.Stat_t)
	if !ok { return nativeLogObservation{}, fmt.Errorf("log identity unavailable") }
	return nativeLogObservation{Present:true, Device:uint64(stat.Dev), Inode:uint64(stat.Ino), Bytes:snapshot.Bytes}, nil
}

// checkNativeLogObservation compares every caller-observed log property.
//
// Example: Identical replacement bytes cannot authorize replay of a different file.
func checkNativeLogObservation(snapshot replayLogSnapshot, expected nativeLogObservation) error {
	actual, err := observeNativeLog(snapshot)
	if err != nil { return err }
	if actual.Present != expected.Present || actual.Device != expected.Device || actual.Inode != expected.Inode || !bytes.Equal(actual.Bytes, expected.Bytes) { return errRefreshRetry }
	return nil
}
