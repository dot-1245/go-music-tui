package artwork

import (
	"fmt"
	"image"
	"io"

	"github.com/dolmen-go/kittyimg"
	"github.com/nfnt/resize"
)

// Render resizes an image by width and writes the kitty graphics protocol.
func Render(writer io.Writer, img image.Image, width uint) error {
	if img == nil {
		return fmt.Errorf("cannot render a nil image")
	}
	if width == 0 {
		return fmt.Errorf("cannot render an image with zero width")
	}
	resized := resize.Resize(width, 0, img, resize.Lanczos3)
	return kittyimg.Fprintln(writer, resized)
}
