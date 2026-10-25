package agent

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"zhiwei/internal/channel"
	"zhiwei/internal/llm"
	"zhiwei/internal/settings"
	"zhiwei/internal/store"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestImageNotes(t *testing.T) {
	if withImageNote("看下这个报错", 1) != "看下这个报错 [图片]" || withImageNote("", 2) != "[图片×2]" || withImageNote("hi", 0) != "hi" {
		t.Fatal("占位格式不对")
	}
	if stripImageNote("看下 [图片] 和 [图片×3]") != "看下  和" {
		t.Fatalf("去占位：%q", stripImageNote("看下 [图片] 和 [图片×3]"))
	}
	if !imagesOnly([]queued{{msg: store.Message{Content: "[图片]"}, images: []channel.Image{{}}}}) ||
		imagesOnly([]queued{{msg: store.Message{Content: "这是啥 [图片]"}, images: []channel.Image{{}}}}) {
		t.Fatal("只发图片才等后续文字")
	}
}

// 模型拒收图片（400）时去图重试，提示词改成「看不到图片」，结果标记 ImagesDropped。
func TestRunDropsRejectedImages(t *testing.T) {
	f := &fakeLLM{reply: func(llm.Request) string { return "请把报错文字发我" }}
	f.fail = func(req llm.Request) error {
		if req.HasImages() {
			return &llm.APIError{Status: http.StatusBadRequest, Body: "image input is not supported"}
		}
		return nil
	}
	cfg := settings.Defaults()
	cfg.Agent.KBPrefetch = false
	b := NewBrain(f, nil, cfg, nil, nil)
	res, err := b.Run(context.Background(), RunInput{Texts: []string{"[图片]"}, Images: []llm.Image{{MIME: "image/png", Data: pngBytes(t)}}, ImageCount: 1})
	if err != nil || res.Reply != "请把报错文字发我" {
		t.Fatalf("应去图重试成功：%+v %v", res, err)
	}
	if !res.ImagesDropped || !res.ImagesRejected || res.Images != 0 || res.LLMCalls != 2 {
		t.Fatalf("结果标记：%+v", res)
	}
	if len(f.reqs) != 2 || !f.reqs[0].HasImages() || f.reqs[1].HasImages() {
		t.Fatal("第一次带图、第二次不带")
	}
	last := f.reqs[1].Messages[len(f.reqs[1].Messages)-1].Content
	if !strings.Contains(last, "看不到图片内容") {
		t.Fatalf("重试时应告诉模型看不到图：%s", last)
	}
	// 已确认模型不收图：下一轮直接不带图，不再白跑一趟
	res, err = b.Run(context.Background(), RunInput{Texts: []string{"[图片]"}, Images: []llm.Image{{MIME: "image/png", Data: pngBytes(t)}}, ImageCount: 1})
	if err != nil || res.LLMCalls != 1 || !res.ImagesDropped || res.ImagesRejected || len(f.reqs) != 3 || f.reqs[2].HasImages() {
		t.Fatalf("第二轮应直接去图：%+v %v", res, err)
	}

	// 去图重试也失败：说明不是图片的问题（如上下文过长），不能记成不收图
	f3 := &fakeLLM{reply: func(llm.Request) string { return "" }, fail: func(llm.Request) error {
		return &llm.APIError{Status: http.StatusBadRequest, Body: "prompt is too long"}
	}}
	b3 := NewBrain(f3, nil, cfg, nil, nil)
	if _, err := b3.Run(context.Background(), RunInput{Texts: []string{"x"}, Images: []llm.Image{{MIME: "image/png"}}, ImageCount: 1}); err == nil {
		t.Fatal("应返回错误")
	}
	if b3.noVision.Load() {
		t.Fatal("去图后仍失败，不该记成模型不收图")
	}

	// 非图片原因的失败不重试
	f2 := &fakeLLM{reply: func(llm.Request) string { return "" }, fail: func(llm.Request) error {
		return &llm.APIError{Status: http.StatusTooManyRequests}
	}}
	if _, err := NewBrain(f2, nil, cfg, nil, nil).Run(context.Background(), RunInput{Texts: []string{"x"}, Images: []llm.Image{{MIME: "image/png"}}, ImageCount: 1}); err == nil || len(f2.reqs) != 1 {
		t.Fatalf("429 不该去图重试：%v %d", err, len(f2.reqs))
	}
}

func TestFetchImagesSkipsFailures(t *testing.T) {
	o := &Orchestrator{log: discardLog()}
	ok := channel.Image{Open: func(context.Context) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(pngBytes(t))), nil }}
	bad := channel.Image{Open: func(context.Context) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("garbage")), nil }}
	got := o.fetchImages(context.Background(), []channel.Image{bad, ok, {}})
	if len(got) != 1 || got[0].MIME != "image/png" {
		t.Fatalf("应只留下能解码的一张：%+v", got)
	}
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
