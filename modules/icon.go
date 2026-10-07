package modules

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// TrayIcon draws the orange play icon as a PNG without external assets.
func TrayIcon() []byte {
	return IconPNG(64)
}

// IconPNG draws the same design at the requested size for application packaging.
func IconPNG(size int) []byte {
	if size < 1 || size > 1024 {
		panic("icon size must be between 1 and 1024")
	}
	const samples = 4
	icon := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var red, green, blue, alpha int
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					px := (float64(x) + (float64(sx)+0.5)/samples) * 64 / float64(size)
					py := (float64(y) + (float64(sy)+0.5)/samples) * 64 / float64(size)
					dx, dy := px-32, py-32
					if dx*dx+dy*dy > 29*29 {
						continue
					}
					r, g, b := 244, 126, 33
					if px >= 25 && px <= 47 && py >= 20+(px-25)*12/22 && py <= 44-(px-25)*12/22 {
						r, g, b = 255, 255, 255
					}
					red += r
					green += g
					blue += b
					alpha += 255
				}
			}
			const total = samples * samples
			icon.SetRGBA(x, y, color.RGBA{uint8((red + total/2) / total), uint8((green + total/2) / total), uint8((blue + total/2) / total), uint8((alpha + total/2) / total)})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, icon); err != nil {
		panic(err)
	}
	return encoded.Bytes()
}
