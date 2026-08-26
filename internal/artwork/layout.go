package artwork

import (
	"image"
	"os"
	"syscall"
	"unsafe"
)

// PixelSize is the physical terminal size when the terminal reports it.
type PixelSize struct {
	Width, Height int
	OK            bool
}

type winsize struct {
	Row, Col       uint16
	Xpixel, Ypixel uint16
}

// GetTermPixelSize reads the physical terminal size through TIOCGWINSZ.
func GetTermPixelSize(file *os.File) PixelSize {
	if file == nil {
		return PixelSize{}
	}
	ws := &winsize{}
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		file.Fd(),
		syscall.TIOCGWINSZ,
		uintptr(unsafe.Pointer(ws)),
	)
	if errno != 0 || ws.Xpixel == 0 || ws.Ypixel == 0 {
		return PixelSize{}
	}
	return PixelSize{Width: int(ws.Xpixel), Height: int(ws.Ypixel), OK: true}
}

// Placement describes the resized width and top-left terminal cell.
type Placement struct {
	Width       uint
	Row, Column int
}

// CalculatePlacement mirrors the main TUI's album-art sizing rules.
func CalculatePlacement(img image.Image, cols, rows int, fullScreen bool, pixels PixelSize) Placement {
	placement := Placement{Width: 250, Row: 2, Column: 2}
	if rows < 25 {
		placement.Width = 180
	}
	if !fullScreen || img == nil {
		return placement
	}

	bounds := img.Bounds()
	srcWidth, srcHeight := bounds.Dx(), bounds.Dy()
	if srcWidth <= 0 || srcHeight <= 0 {
		return placement
	}
	if pixels.OK && cols > 0 && rows > 0 {
		scale := float64(pixels.Width) / float64(srcWidth)
		if heightScale := float64(pixels.Height) / float64(srcHeight); heightScale < scale {
			scale = heightScale
		}
		placement.Width = uint(float64(srcWidth) * scale)
		cellWidth := float64(pixels.Width) / float64(cols)
		cellHeight := float64(pixels.Height) / float64(rows)
		if cellWidth > 0 && cellHeight > 0 {
			displayCellsWidth := float64(placement.Width) / cellWidth
			displayCellsHeight := float64(srcHeight) * scale / cellHeight
			placement.Column = int(float64(cols)/2-displayCellsWidth/2) + 1
			placement.Row = int(float64(rows)/2-displayCellsHeight/2) + 1
		}
	} else if cols > 0 {
		placement.Width = uint(cols * 9)
	}
	if placement.Width == 0 {
		placement.Width = 1
	}
	if placement.Column < 1 {
		placement.Column = 1
	}
	if placement.Row < 1 {
		placement.Row = 1
	}
	return placement
}
