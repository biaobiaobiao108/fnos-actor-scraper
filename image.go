package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"net"
	"net/url"
	"strings"

	_ "golang.org/x/image/webp"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
)

const maxImageDownload int64 = 10 << 20
const maxImageOutput = 4 << 20
const maxImagePixels int64 = 16_000_000

func fetchPortrait(ctx context.Context, upstream *Upstream, address string) ([]byte, error) {
	target, err := url.Parse(address)
	if err != nil || target.Scheme != "https" || target.Hostname() == "" || !isPublicHost(target.Hostname()) {
		return nil, fmt.Errorf("头像地址必须是公网 HTTPS 地址")
	}
	body, err := upstream.Get(ctx, target.String(), map[string]string{"Accept": "image/*"}, maxImageDownload)
	if err != nil {
		return nil, fmt.Errorf("头像下载失败：%w", err)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("无法识别头像图片：%w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxImagePixels {
		return nil, fmt.Errorf("头像原图超过 1600 万像素限制")
	}
	decoded, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("头像解码失败：%w", err)
	}
	resized := cropResize(decoded, 640, 960)
	var output bytes.Buffer
	output.Grow(256 * 1024)
	if err := jpeg.Encode(&output, resized, &jpeg.Options{Quality: 88}); err != nil {
		return nil, err
	}
	if output.Len() > maxImageOutput {
		return nil, fmt.Errorf("处理后的头像仍超过飞牛 4 MiB 上传限制")
	}
	return output.Bytes(), nil
}

func cropResize(source image.Image, width, height int) *image.NRGBA {
	bounds := source.Bounds()
	sourceW, sourceH := bounds.Dx(), bounds.Dy()
	wantedRatio, sourceRatio := float64(width)/float64(height), float64(sourceW)/float64(sourceH)
	cropW, cropH := float64(sourceW), float64(sourceH)
	if sourceRatio > wantedRatio {
		cropW = float64(sourceH) * wantedRatio
	} else {
		cropH = float64(sourceW) / wantedRatio
	}
	left := float64(bounds.Min.X) + (float64(sourceW)-cropW)/2
	top := float64(bounds.Min.Y) + (float64(sourceH)-cropH)/2
	destination := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		sy := top + (float64(y)+0.5)*cropH/float64(height) - 0.5
		y0 := clamp(int(sy), bounds.Min.Y, bounds.Max.Y-1)
		y1 := clamp(y0+1, bounds.Min.Y, bounds.Max.Y-1)
		fy := max(0, min(1, sy-float64(y0)))
		for x := 0; x < width; x++ {
			sx := left + (float64(x)+0.5)*cropW/float64(width) - 0.5
			x0 := clamp(int(sx), bounds.Min.X, bounds.Max.X-1)
			x1 := clamp(x0+1, bounds.Min.X, bounds.Max.X-1)
			fx := max(0, min(1, sx-float64(x0)))
			c00 := opaqueRGBA(source.At(x0, y0))
			c10 := opaqueRGBA(source.At(x1, y0))
			c01 := opaqueRGBA(source.At(x0, y1))
			c11 := opaqueRGBA(source.At(x1, y1))
			pixel := color.NRGBA{R: bilinear(c00.R, c10.R, c01.R, c11.R, fx, fy), G: bilinear(c00.G, c10.G, c01.G, c11.G, fx, fy), B: bilinear(c00.B, c10.B, c01.B, c11.B, fx, fy), A: 255}
			destination.SetNRGBA(x, y, pixel)
		}
	}
	return destination
}

func opaqueRGBA(value color.Color) color.NRGBA {
	r, g, b, a := value.RGBA()
	// image.Color.RGBA returns premultiplied components; flatten transparency to white.
	return color.NRGBA{R: uint8(min(uint32(255), (r+(65535-a)+128)/257)), G: uint8(min(uint32(255), (g+(65535-a)+128)/257)), B: uint8(min(uint32(255), (b+(65535-a)+128)/257)), A: 255}
}

func bilinear(a, b, c, d uint8, fx, fy float64) uint8 {
	upper := float64(a)*(1-fx) + float64(b)*fx
	lower := float64(c)*(1-fx) + float64(d)*fx
	return uint8(max(0, min(255, int(upper*(1-fy)+lower*fy+0.5))))
}

func clamp(value, minimum, maximum int) int { return max(minimum, min(maximum, value)) }

func isPublicHost(host string) bool {
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return false
	}
	if address := net.ParseIP(host); address != nil {
		return !address.IsPrivate() && !address.IsLoopback() && !address.IsLinkLocalUnicast() && !address.IsLinkLocalMulticast() && !address.IsUnspecified() && !address.IsMulticast()
	}
	return !strings.Contains(host, ":")
}
