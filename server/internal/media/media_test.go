package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func encodePNG(t *testing.T, m image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestNormalizeSmallPNGKept(t *testing.T) {
	m := image.NewRGBA(image.Rect(0, 0, 40, 20))
	raw := encodePNG(t, m)
	img, err := Normalize(raw)
	if err != nil {
		t.Fatal(err)
	}
	if img.MIME != "image/png" || !bytes.Equal(img.Data, raw) || img.Width != 40 || img.Height != 20 {
		t.Fatalf("小 PNG 应原样保留：%s %dx%d", img.MIME, img.Width, img.Height)
	}
}

func TestNormalizeLargeResizedToJPEG(t *testing.T) {
	m := image.NewRGBA(image.Rect(0, 0, 4000, 1000))
	for x := 0; x < 4000; x++ {
		for y := 0; y < 1000; y++ {
			m.Set(x, y, color.RGBA{uint8(x), uint8(y), 0, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, m, nil); err != nil {
		t.Fatal(err)
	}
	img, err := Normalize(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if img.MIME != "image/jpeg" || img.Width != MaxSide || img.Height != 1000*MaxSide/4000 {
		t.Fatalf("长边应缩到 %d：%s %dx%d", MaxSide, img.MIME, img.Width, img.Height)
	}
	if _, err := jpeg.Decode(bytes.NewReader(img.Data)); err != nil {
		t.Fatalf("输出应是合法 JPEG：%v", err)
	}
}

func TestNormalizeTransparentLarge(t *testing.T) {
	m := image.NewNRGBA(image.Rect(0, 0, 2000, 2000)) // 全透明
	img, err := Normalize(encodePNG(t, m))
	if err != nil {
		t.Fatal(err)
	}
	if img.Width != MaxSide || img.Height != MaxSide {
		t.Fatalf("应缩放：%dx%d", img.Width, img.Height)
	}
}

func TestNormalizeRejectsGarbage(t *testing.T) {
	if _, err := Normalize([]byte("not an image")); err == nil {
		t.Fatal("非图片应报错")
	}
}

// 文件很小、IHDR 却声明 60000×60000：解码前就该拒掉，不能真去分配像素缓冲
func TestNormalizeRejectsHugeDimensions(t *testing.T) {
	raw := encodePNG(t, image.NewGray(image.Rect(0, 0, 1, 1)))
	// PNG 签名 8 字节 + 长度 4 + "IHDR" 4，随后是宽、高，IHDR 数据 13 字节，之后是 CRC
	binary.BigEndian.PutUint32(raw[16:], 60000)
	binary.BigEndian.PutUint32(raw[20:], 60000)
	binary.BigEndian.PutUint32(raw[29:], crc32.ChecksumIEEE(raw[12:29]))
	if _, err := Normalize(raw); !errors.Is(err, ErrTooMany) {
		t.Fatalf("超大尺寸应返回 ErrTooMany：%v", err)
	}
}

func TestReadLimit(t *testing.T) {
	if _, err := Read(bytes.NewReader(make([]byte, MaxDownload+1))); err != ErrTooLarge {
		t.Fatalf("超限应返回 ErrTooLarge：%v", err)
	}
}
