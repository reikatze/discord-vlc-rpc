package modules

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// AppIconICNS packages the shared drawing in macOS's PNG-based icon container.
func AppIconICNS() []byte {
	var body bytes.Buffer
	for _, entry := range []struct {
		tag  string
		size int
	}{
		{"icp4", 16}, {"icp5", 32}, {"icp6", 64}, {"ic07", 128},
		{"ic08", 256}, {"ic09", 512}, {"ic10", 1024},
	} {
		png := IconPNG(entry.size)
		body.WriteString(entry.tag)
		binary.Write(&body, binary.BigEndian, uint32(len(png)+8))
		body.Write(png)
	}
	var result bytes.Buffer
	result.WriteString("icns")
	binary.Write(&result, binary.BigEndian, uint32(body.Len()+8))
	result.Write(body.Bytes())
	return result.Bytes()
}

// AppIconWindows emits a COFF resource object for Go's Windows linker. Both
// RT_ICON (3) and RT_GROUP_ICON (14) use the same drawing as the tray. No
// external resource compiler or checked-in binary asset is needed.
// Format: https://learn.microsoft.com/en-us/windows/win32/debug/pe-format
func AppIconWindows(arch string) ([]byte, error) {
	machine, relocation := uint16(0x8664), uint16(3) // AMD64, ADDR32NB
	switch arch {
	case "amd64":
	case "arm64":
		machine, relocation = 0xaa64, 2 // ARM64, ADDR32NB
	default:
		return nil, fmt.Errorf("unsupported Windows icon architecture %q", arch)
	}
	sizes := []int{16, 32, 48, 64, 128, 256}
	payloads := make([][]byte, len(sizes)+1)
	group := make([]byte, 6+14*len(sizes))
	put16 := binary.LittleEndian.PutUint16
	put32 := binary.LittleEndian.PutUint32
	put16(group[2:], 1)
	put16(group[4:], uint16(len(sizes)))
	for i, size := range sizes {
		payloads[i] = IconPNG(size)
		entry := group[6+14*i:]
		entry[0], entry[1] = byte(size), byte(size) // zero denotes 256
		put16(entry[4:], 1)
		put16(entry[6:], 32)
		put32(entry[8:], uint32(len(payloads[i])))
		put16(entry[12:], uint16(i+1))
	}
	payloads[len(sizes)] = group
	// Resource directory offsets are relative to the start of .rsrc.
	data := make([]byte, 32)
	alloc := func(n int) int { offset := len(data); data = append(data, make([]byte, n)...); return offset }
	rootIcon := alloc(16 + 8*len(sizes))
	rootGroup := alloc(24)
	put16(data[14:], 2)
	put32(data[16:], 3)
	put32(data[20:], uint32(rootIcon)|0x80000000)
	put32(data[24:], 14)
	put32(data[28:], uint32(rootGroup)|0x80000000)
	put16(data[rootIcon+14:], uint16(len(sizes)))
	put16(data[rootGroup+14:], 1)
	var relocations []uint32
	for i, payload := range payloads {
		parent, id := rootIcon+16+i*8, i+1
		if i == len(sizes) {
			parent, id = rootGroup+16, 1
		}
		language := alloc(24)
		put32(data[parent:], uint32(id))
		put32(data[parent+4:], uint32(language)|0x80000000)
		put16(data[language+14:], 1)
		put32(data[language+16:], 0) // language-neutral
		entry := alloc(16)
		put32(data[language+20:], uint32(entry))
		put32(data[entry+4:], uint32(len(payload)))
		start := alloc((len(payload) + 3) &^ 3)
		put32(data[entry:], uint32(start))
		copy(data[start:], payload)
		relocations = append(relocations, uint32(entry))
	}
	// One section, one section symbol, plus the four-byte empty string table.
	relocStart := 60 + len(data)
	symbolStart := relocStart + 10*len(relocations)
	object := make([]byte, symbolStart+18+4)
	put16(object, machine)
	put16(object[2:], 1)
	put32(object[8:], uint32(symbolStart))
	put32(object[12:], 1)
	copy(object[20:], ".rsrc")
	put32(object[36:], uint32(len(data)))
	put32(object[40:], 60)
	put32(object[44:], uint32(relocStart))
	put16(object[52:], uint16(len(relocations)))
	put32(object[56:], 0x40000040) // initialized, readable data
	copy(object[60:], data)
	for i, offset := range relocations {
		entry := object[relocStart+10*i:]
		put32(entry, offset)
		put16(entry[8:], relocation)
	}
	copy(object[symbolStart:], ".rsrc")
	put16(object[symbolStart+12:], 1)
	object[symbolStart+16] = 3 // static section symbol
	put32(object[symbolStart+18:], 4)
	return object, nil
}
