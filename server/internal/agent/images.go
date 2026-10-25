package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"zhiwei/internal/channel"
	"zhiwei/internal/llm"
	"zhiwei/internal/media"
	"zhiwei/internal/store"
)

// 图片识别：客户 @ 机器人或私聊时附带的图片，在会话 worker 里下载、缩放，存一份给后台查看，
// 设置开启识图时随本轮 user 消息交给模型。消息正文里留「[图片]」占位，历史与群时间线只有文字。
//
// 只在问答流程里下载：群里没 @ 机器人的图片不下载（主动回复、反思、问题排行都只看文字）。

const (
	imagePlaceholder = "[图片]"
	// imageWait 只发了图没说话时多等一会儿：用户常常先发截图、再补一句问题
	imageWait = 3 * time.Second
	// imageRetention 图片在库里保留的时长
	imageRetention = 30 * 24 * time.Hour
)

var imageMark = regexp.MustCompile(`\[图片(×\d+)?\]`)

// withImageNote 消息正文 + 图片占位：「看下这个报错 [图片]」「[图片×2]」。
func withImageNote(text string, n int) string {
	if n <= 0 {
		return text
	}
	mark := imagePlaceholder
	if n > 1 {
		mark = fmt.Sprintf("[图片×%d]", n)
	}
	if text = strings.TrimSpace(text); text == "" {
		return mark
	}
	return text + " " + mark
}

func stripImageNote(text string) string {
	return strings.TrimSpace(imageMark.ReplaceAllString(text, ""))
}

// fetchImages 并发下载并缩放，按原顺序返回成功的；失败的记日志跳过（图片只是锦上添花）。
func (o *Orchestrator) fetchImages(ctx context.Context, refs []channel.Image) []media.Image {
	out := make([]*media.Image, len(refs))
	var wg sync.WaitGroup
	for i, ref := range refs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			img, err := fetchImage(ctx, ref)
			if err != nil {
				o.log.Warn("下载图片失败，已跳过", "err", err)
				return
			}
			out[i] = &img
		}()
	}
	wg.Wait()
	var list []media.Image
	for _, img := range out {
		if img != nil {
			list = append(list, *img)
		}
	}
	return list
}

func fetchImage(ctx context.Context, ref channel.Image) (media.Image, error) {
	if ref.Open == nil {
		return media.Image{}, errors.New("渠道没有提供下载入口")
	}
	if ref.Size > media.MaxDownload {
		return media.Image{}, media.ErrTooLarge
	}
	ctx, cancel := context.WithTimeout(ctx, media.FetchTimeout)
	defer cancel()
	body, err := ref.Open(ctx)
	if err != nil {
		return media.Image{}, err
	}
	defer body.Close()
	raw, err := media.Read(body)
	if err != nil {
		return media.Image{}, err
	}
	return media.Normalize(raw)
}

func (o *Orchestrator) saveImages(ctx context.Context, msgID int64, imgs []media.Image) {
	if len(imgs) == 0 {
		return
	}
	rows := make([]store.MessageImage, len(imgs))
	for i, im := range imgs {
		rows[i] = store.MessageImage{MIME: im.MIME, Width: im.Width, Height: im.Height, Data: im.Data}
	}
	if err := o.st.AddMessageImages(ctx, msgID, rows); err != nil {
		o.log.Warn("保存图片失败", "msg", msgID, "err", err)
	}
}

// keepImages 不进入问答（人工接待中）的消息：图片只下载保存，留给后台人工查看。
func (o *Orchestrator) keepImages(msgID int64, refs []channel.Image) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*media.FetchTimeout)
	defer cancel()
	o.saveImages(ctx, msgID, o.fetchImages(ctx, refs))
}

// batchImages 下载本批消息的图片、逐条保存，返回交给模型的图片（最多 channel.MaxImages 张）与客户本批发来的总张数。
func (o *Orchestrator) batchImages(ctx context.Context, batch []queued) (imgs []llm.Image, sent int) {
	for _, q := range batch {
		if len(q.images) == 0 {
			continue
		}
		sent += len(q.images)
		got := o.fetchImages(ctx, q.images)
		o.saveImages(ctx, q.msg.ID, got)
		for _, im := range got {
			if len(imgs) < channel.MaxImages {
				imgs = append(imgs, llm.Image{MIME: im.MIME, Data: im.Data})
			}
		}
	}
	return imgs, sent
}

// imagesOnly 本批全是不带文字的图片。
func imagesOnly(batch []queued) bool {
	for _, q := range batch {
		if len(q.images) == 0 || stripImageNote(q.msg.Content) != "" {
			return false
		}
	}
	return len(batch) > 0
}

// imageRejected 上游因为图片拒收请求（模型不支持识图、图片格式 / 大小不被接受等）：去掉图片重试一次。
func imageRejected(err error) bool {
	var ae *llm.APIError
	if !errors.As(err, &ae) {
		return false
	}
	switch ae.Status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
		return true
	}
	return false
}

// imageNote 本轮图片的说明，放在客户原文块的开头。
func imageNote(seen, total int) string {
	switch {
	case total == 0:
		return ""
	case seen == total:
		return fmt.Sprintf("【客户随消息发来 %d 张图片，附在本条消息末尾】", total)
	case seen > 0:
		return fmt.Sprintf("【客户发来 %d 张图片，附在本条消息末尾的是其中 %d 张，其余没能加载】", total, seen)
	}
	return fmt.Sprintf("【客户发来 %d 张图片，但你看不到图片内容；回答需要图片里的信息时，请客户用文字描述（如报错原文）】", total)
}
