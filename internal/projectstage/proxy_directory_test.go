package projectstage

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"
)

func TestFileProxyDirectoryBoundsBeforeZIPAllocation(t *testing.T) {
	data := proxyArchive(t, "v1.0.0")
	for _, prefix := range [][]byte{nil, []byte("prepended archive bytes")} {
		candidate := append(append([]byte(nil), prefix...), data...)
		reader, err := zip.NewReader(bytes.NewReader(candidate), int64(len(candidate)))
		if err != nil || len(reader.File) != 2 {
			t.Fatal("invalid control archive", err)
		}
		if err := checkProxyDirectory(t.Context(), candidate, 2); err != nil {
			t.Fatal("bounded archive rejected", err)
		}
		if err := checkProxyDirectory(t.Context(), candidate, 1); !errors.Is(err, ErrStage) {
			t.Fatal("archive record bound was ignored", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := checkProxyDirectory(ctx, data, 2); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled directory scan continued", err)
	}
	for size := range len(data) {
		if err := checkProxyDirectory(t.Context(), data[:size], 2); err == nil {
			t.Fatalf("truncated archive accepted at %d", size)
		}
	}
}

func TestFileProxyDirectoryRejectsWrappedRecordCount(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	const records = 1<<16 + 2
	for range records {
		if _, err := writer.Create("example.com/dependency@v1.0.0/empty/"); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data := buffer.Bytes()
	// Go accepts wrapped 16-bit counts without ZIP64 metadata. The entry budget
	// must be checked against actual directory records, not the advertised two.
	end := len(data) - 22
	binary.LittleEndian.PutUint16(data[end+8:], 2)
	binary.LittleEndian.PutUint16(data[end+10:], 2)
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(reader.File) != records {
		t.Fatal("Go did not accept the wrapped control archive", err)
	}
	if err := checkProxyDirectory(t.Context(), data, MaximumEntries-1); !errors.Is(err, ErrStage) {
		t.Fatal("wrapped directory count bypassed allocation bound", err)
	}
	if err := checkProxyDirectory(t.Context(), data, records); err != nil {
		t.Fatal("actual record count differs from Go", err)
	}
}

func TestFileProxyDirectoryBoundsZIP64(t *testing.T) {
	data := proxyArchive(t, "v1.0.0")
	end := len(data) - 22
	var extended [56 + 20]byte
	binary.LittleEndian.PutUint32(extended[:], 0x06064b50)
	binary.LittleEndian.PutUint64(extended[4:], 44)
	binary.LittleEndian.PutUint16(extended[12:], 45)
	binary.LittleEndian.PutUint16(extended[14:], 45)
	binary.LittleEndian.PutUint64(extended[24:], 2)
	binary.LittleEndian.PutUint64(extended[32:], 2)
	binary.LittleEndian.PutUint64(extended[40:], uint64(binary.LittleEndian.Uint32(data[end+12:])))
	binary.LittleEndian.PutUint64(extended[48:], uint64(binary.LittleEndian.Uint32(data[end+16:])))
	binary.LittleEndian.PutUint32(extended[56:], 0x07064b50)
	binary.LittleEndian.PutUint64(extended[64:], uint64(end))
	binary.LittleEndian.PutUint32(extended[72:], 1)
	endRecord := append([]byte(nil), data[end:]...)
	binary.LittleEndian.PutUint16(endRecord[8:], 0xffff)
	binary.LittleEndian.PutUint16(endRecord[10:], 0xffff)
	data = append(append(append([]byte(nil), data[:end]...), extended[:]...), endRecord...)
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(reader.File) != 2 {
		t.Fatal("invalid ZIP64 control archive", err)
	}
	if err := checkProxyDirectory(t.Context(), data, 2); err != nil {
		t.Fatal("bounded ZIP64 archive rejected", err)
	}
	if err := checkProxyDirectory(t.Context(), data, 1); !errors.Is(err, ErrStage) {
		t.Fatal("ZIP64 record bound was ignored", err)
	}
	for _, position := range []uint64{uint64(len(data)), ^uint64(0)} {
		binary.LittleEndian.PutUint64(data[len(data)-22-20+8:], position)
		if err := checkProxyDirectory(t.Context(), data, 2); err == nil {
			t.Fatal("invalid ZIP64 location was accepted")
		}
	}
}
