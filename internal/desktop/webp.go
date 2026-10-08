package desktop

import (
	"bytes"
	"fmt"
	"image"

	"golang.org/x/image/webp"
)

// decodeWebP decodes a (first frame of a) WebP image.
func decodeWebP(data []byte) (image.Image, error) {
	img, err := webp.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("WebP 解码失败: %w", err)
	}
	return img, nil
}
