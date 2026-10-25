// Package media 客户发来的图片：限量读取、解码、缩放、重新编码成模型都认的 JPEG / PNG。
//
// 缩放到长边 ≤ MaxSide：各家视觉模型都会把大图压到 1500px 左右再看，原图只会白占 token 和请求体积；
// 后台查看与当轮识别共用同一份缩放后的字节。纯 Go 实现，不依赖 cgo / 外部命令。
package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // 注册 gif 解码（image.Decode 只取第一帧）
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"time"

	_ "golang.org/x/image/bmp" // 注册 bmp 解码
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // 注册 webp 解码
)

const (
	// MaxDownload 单张原图字节上限（超出不下载）
	MaxDownload = 10 << 20
	// MaxSide 缩放后长边像素上限
	MaxSide = 1568
	// keepPNG 未缩放且不超过该大小的 PNG 原样保留（截图里的文字更清楚），否则转 JPEG
	keepPNG     = 1 << 20
	jpegQuality = 85
	// FetchTimeout 单张下载超时
	FetchTimeout = 15 * time.Second
	// MaxPixels 解码前按文件头声明的宽 × 高拦截：解码要按全尺寸分配像素缓冲（RGBA 每像素 4 字节），
	// 几 MB 的文件可以声明出几万像素见方的尺寸，不拦就是一张图吃掉十几 GB 内存
	MaxPixels = 40_000_000
)

var (
	ErrTooLarge = errors.New("图片超过 10MB")
	ErrTooMany  = errors.New("图片像素过大")
	decodeSlots = make(chan struct{}, 2) // 同时解码的张数上限，压住多个会话一起发大图时的内存峰值
)

type Image struct {
	MIME   string
	Data   []byte
	Width  int
	Height int
}

// Read 从 r 读取原图，超过 MaxDownload 返回 ErrTooLarge。
func Read(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxDownload+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxDownload {
		return nil, ErrTooLarge
	}
	return raw, nil
}

// HTTPClient 下载平台图片用（QQ 附件地址、Telegram 文件地址）。
var HTTPClient = &http.Client{Timeout: FetchTimeout}

// Get GET 一个图片地址并返回响应体（调用方负责关闭）；非 2xx 或声明的长度超限直接报错。
func Get(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, fmt.Errorf("下载图片 HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > MaxDownload {
		resp.Body.Close()
		return nil, ErrTooLarge
	}
	return resp.Body, nil
}

// Normalize 解码 JPEG / PNG / GIF（取第一帧）/ WebP，长边缩到 MaxSide 以内后重新编码。
func Normalize(raw []byte) (Image, error) {
	conf, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return Image{}, fmt.Errorf("无法识别的图片格式：%w", err)
	}
	if conf.Width <= 0 || conf.Height <= 0 {
		return Image{}, errors.New("图片尺寸为 0")
	}
	if int64(conf.Width)*int64(conf.Height) > MaxPixels {
		return Image{}, fmt.Errorf("%w：%d×%d", ErrTooMany, conf.Width, conf.Height)
	}
	decodeSlots <- struct{}{}
	defer func() { <-decodeSlots }()
	src, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return Image{}, fmt.Errorf("无法识别的图片格式：%w", err)
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return Image{}, errors.New("图片尺寸为 0")
	}
	resized := false
	if long := max(w, h); long > MaxSide {
		nw, nh := max(w*MaxSide/long, 1), max(h*MaxSide/long, 1)
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Src, nil)
		src, w, h, resized = dst, nw, nh, true
	}
	if format == "png" && !resized && len(raw) <= keepPNG {
		return Image{MIME: "image/png", Data: raw, Width: w, Height: h}, nil
	}
	var buf bytes.Buffer
	if opaque(src) {
		err = jpeg.Encode(&buf, src, &jpeg.Options{Quality: jpegQuality})
		if err == nil {
			return Image{MIME: "image/jpeg", Data: buf.Bytes(), Width: w, Height: h}, nil
		}
	} else if err = png.Encode(&buf, src); err == nil && buf.Len() <= 2*keepPNG {
		return Image{MIME: "image/png", Data: buf.Bytes(), Width: w, Height: h}, nil
	}
	// 带透明的大图：铺白底转 JPEG
	flat := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(flat, flat.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), src, src.Bounds().Min, draw.Over)
	buf.Reset()
	if err := jpeg.Encode(&buf, flat, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return Image{}, err
	}
	return Image{MIME: "image/jpeg", Data: buf.Bytes(), Width: w, Height: h}, nil
}

// opaque 图片是否没有透明像素（有就不能直接转 JPEG）。
func opaque(m image.Image) bool {
	if o, ok := m.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}
	return false
}
