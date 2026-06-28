//go:build windows

package icon

import "encoding/binary"

// Data returns the tray icon as an ICO. Windows trays require ICO; since Vista
// an ICO directory entry may wrap a PNG payload directly, so we wrap pngBytes()
// in a one-entry ICO container.
func Data() []byte {
	png := pngBytes()

	buf := make([]byte, 0, 22+len(png))
	// ICONDIR header.
	hdr := make([]byte, 6)
	binary.LittleEndian.PutUint16(hdr[0:], 0) // reserved
	binary.LittleEndian.PutUint16(hdr[2:], 1) // type: icon
	binary.LittleEndian.PutUint16(hdr[4:], 1) // image count
	buf = append(buf, hdr...)

	// ICONDIRENTRY.
	entry := make([]byte, 16)
	entry[0] = byte(size % 256)                                // width  (0 == 256)
	entry[1] = byte(size % 256)                                // height
	entry[2] = 0                                               // palette
	entry[3] = 0                                               // reserved
	binary.LittleEndian.PutUint16(entry[4:], 1)                // color planes
	binary.LittleEndian.PutUint16(entry[6:], 32)               // bits per pixel
	binary.LittleEndian.PutUint32(entry[8:], uint32(len(png))) // payload size
	binary.LittleEndian.PutUint32(entry[12:], 22)              // payload offset
	buf = append(buf, entry...)

	return append(buf, png...)
}
