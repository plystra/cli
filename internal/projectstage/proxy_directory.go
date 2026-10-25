package projectstage

import (
	"context"
	"encoding/binary"
)

// Bound central-directory records before archive/zip allocates a File for each
// one. ZIP counts can wrap at 16 bits, so the advertised count is insufficient.
func checkProxyDirectory(ctx context.Context, data []byte, maximum int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	end := -1
	// Match archive/zip's two tail searches, including its first signature and
	// truncated-comment behavior, so a comment cannot select another directory.
	for _, size := range []int{1024, 65 * 1024} {
		start := max(0, len(data)-size)
		for offset := len(data) - 22; offset >= start; offset-- {
			if binary.LittleEndian.Uint32(data[offset:]) != 0x06054b50 {
				continue
			}
			if int(binary.LittleEndian.Uint16(data[offset+20:])) <= len(data)-offset-22 {
				end = offset
			}
			break
		}
		if end >= 0 || start == 0 {
			break
		}
	}
	if end < 0 {
		return ErrSourceIntegrity
	}
	records := uint64(binary.LittleEndian.Uint16(data[end+10:]))
	size := uint64(binary.LittleEndian.Uint32(data[end+12:]))
	offset := uint64(binary.LittleEndian.Uint32(data[end+16:]))
	if records == 0xffff || size == 0xffff || offset == 0xffffffff {
		locator := end - 20
		if locator >= 0 && binary.LittleEndian.Uint32(data[locator:]) == 0x07064b50 &&
			binary.LittleEndian.Uint32(data[locator+4:]) == 0 && binary.LittleEndian.Uint32(data[locator+16:]) == 1 {
			position := binary.LittleEndian.Uint64(data[locator+8:])
			if len(data) < 56 || position > uint64(len(data)-56) || binary.LittleEndian.Uint32(data[position:]) != 0x06064b50 {
				return ErrSourceIntegrity
			}
			end = int(position)
			records = binary.LittleEndian.Uint64(data[end+32:])
			size = binary.LittleEndian.Uint64(data[end+40:])
			offset = binary.LittleEndian.Uint64(data[end+48:])
		}
	}
	if maximum < 0 || records > uint64(maximum) {
		return ErrStage
	}
	if size > uint64(end) {
		return ErrSourceIntegrity
	}
	start := end - int(size)
	if start >= len(data) {
		return ErrSourceIntegrity
	}
	if err := countProxyDirectory(ctx, data, start, maximum); err != nil {
		return err
	}
	// archive/zip also tries the unadjusted offset for prepended archives with
	// a positive base offset. Bound both possibilities before invoking it.
	if offset < uint64(start) {
		return countProxyDirectory(ctx, data, int(offset), maximum)
	}
	return nil
}

func countProxyDirectory(ctx context.Context, data []byte, offset, maximum int) error {
	for count := 0; len(data)-offset >= 46 && binary.LittleEndian.Uint32(data[offset:]) == 0x02014b50; count++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if count >= maximum {
			return ErrStage
		}
		length := 46 + int(binary.LittleEndian.Uint16(data[offset+28:])) +
			int(binary.LittleEndian.Uint16(data[offset+30:])) + int(binary.LittleEndian.Uint16(data[offset+32:]))
		if length > len(data)-offset {
			break
		}
		offset += length
	}
	return nil
}
