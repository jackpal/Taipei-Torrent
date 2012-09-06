package nettools

import "fmt"

func BinaryToDottedPort(port string) string {
	return fmt.Sprintf("%d.%d.%d.%d:%d", port[0], port[1], port[2], port[3],
		(uint16(port[4])<<8)|uint16(port[5]))
}

// 97.98.99.100:25958 becames "abcdef".
func DottedPortToBinary(b string) string {
	a := make([]byte, 6, 6)
	var c uint16

	fmt.Sscanf(b, "%d.%d.%d.%d:%d", &a[0], &a[1], &a[2], &a[3], &c)
	a[4] = uint8(c >> 8)
	a[5] = uint8(c)

	return string(a)
}
