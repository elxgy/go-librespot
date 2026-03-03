package go_librespot

import (
	"strings"

	metadatapb "github.com/elxgy/go-librespot/proto/spotify/metadata"
)

func GetBestImageIdForSize(images []*metadatapb.Image, size string) []byte {
	if len(images) == 0 {
		return nil
	}
	if size == "" {
		size = "DEFAULT"
	}
	sizeKey := strings.ToUpper(size)
	imageSizeVal, ok := metadatapb.Image_Size_value[sizeKey]
	if !ok {
		imageSizeVal = int32(metadatapb.Image_DEFAULT)
	}
	imageSize := metadatapb.Image_Size(imageSizeVal)
	dist := func(a metadatapb.Image_Size) int {
		diff := int(a) - int(imageSize)
		if diff < 0 {
			return -diff
		}
		return diff
	}
	var bestImage *metadatapb.Image
	for _, img := range images {
		if img.Size == nil {
			continue
		}
		if *img.Size == imageSize {
			return img.FileId
		}
		if bestImage == nil || dist(*img.Size) < dist(*bestImage.Size) {
			bestImage = img
		}
	}
	if bestImage != nil {
		return bestImage.FileId
	}
	return images[0].FileId
}
