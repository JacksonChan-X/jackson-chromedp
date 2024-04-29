package entity

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"jackson-chromedp/utils"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

// Node 重定向节点
type Node struct {
	Url string // URL
	Way string // 重定向方式
}

type Tab struct {
	Parent *Browser
	Ctx    context.Context
	Cancel context.CancelFunc
	URL    string

	Events            []any
	FrameID           cdp.FrameID
	LoaderID          cdp.LoaderID
	LastRequestID     network.RequestID
	LastLoaderID      cdp.LoaderID
	FirstResponse     *network.Response
	LastResponse      *network.Response
	HijackRequestFunc HijackRequestFunc
	Header            Header
}

type Header struct {
	UserAgent string
}

type TabOption func(*Tab)

func WithUserAgent(ua string) TabOption {
	return func(t *Tab) {
		if len(ua) == 0 {
			ua = UserAgent
		}
		t.Header.UserAgent = ua
	}
}

func (t *Tab) Close() {
	chromedp.Cancel(t.Ctx)
	t.Cancel()

	t.Parent.Tabs.Delete(t)
	<-t.Parent.Pool
}

func (t *Tab) BuildHooks() chromedp.Tasks {
	tasks := make([]chromedp.Action, 0)

	// tasks = append(tasks, chromedp.EmulateViewport(1920, 1080))

	tasks = append(tasks, t.FetchAllEvents(t.HandleJSDialog())) // 可注入自定义监听事件,同时获取所有事件

	// tasks = append(tasks, t.FetchAllEvents())

	tasks = append(tasks, t.Navigate())

	return tasks
}

func (t *Tab) Navigate() chromedp.ActionFunc {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		var errorText string
		t.FrameID, t.LoaderID, errorText, err = page.Navigate(t.URL).Do(ctx)
		if err != nil {
			return err
		}
		if errorText != "" {
			return errors.New(errorText)
		}
		return nil
	})
}

func (t *Tab) FetchAllEvents(funcs ...func(e any)) chromedp.ActionFunc {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		chromedp.ListenTarget(ctx, func(ev interface{}) {
			for _, f := range funcs {
				f(ev)
			}
			t.Events = append(t.Events, ev)
		})
		return nil
	})
}

func (t *Tab) waitNetworkAlmostIdle(timeout time.Duration) {
	defer t.StopLoading()

	lctx, cancel := context.WithTimeout(t.Ctx, timeout)
	defer cancel()

	chromedp.ListenTarget(lctx, func(v interface{}) {
		ev, ok := v.(*page.EventLifecycleEvent)
		if ok && ev.LoaderID == t.LoaderID && strings.EqualFold(ev.Name, "networkAlmostIdle") {
			cancel()
			return
		}
	})
	<-lctx.Done()
}

func (t *Tab) StopLoading() error {
	return chromedp.Run(t.Ctx, chromedp.Stop())
}

func (t *Tab) FetchRedirectNodes() []Node {
	nodes := make([]Node, 0)
	for _, ev := range t.Events {
		switch e := ev.(type) {
		case *network.EventResponseReceived:
			if e.Type == network.ResourceTypeDocument {
				t.LastResponse = e.Response
				if t.FrameID == e.FrameID {
					t.FirstResponse = e.Response
				}
			}
		case *network.EventRequestWillBeSent:
			if e.Type == network.ResourceTypeDocument {
				t.LastRequestID = e.RequestID
				t.LastLoaderID = e.LoaderID
				nodes = append(nodes, Node{
					Url: e.Request.URL,
					Way: e.Initiator.Type.String(),
				})
			}
		}
	}
	return utils.Unique[Node](nodes)
}

// HandleJavaScriptDialog 关闭js对话框
func (t *Tab) HandleJavaScriptDialog() {
	chromedp.ListenTarget(t.Ctx, func(ev interface{}) {
		switch ev.(type) {
		case *page.EventJavascriptDialogOpening:
			go chromedp.Run(t.Ctx, emulation.SetUserAgentOverride(t.Header.UserAgent), page.HandleJavaScriptDialog(false))
		}
	})
}

func (t *Tab) HandleJSDialog() func(e any) {
	return func(e any) {
		switch e.(type) {
		case *page.EventJavascriptDialogOpening:
			go chromedp.Run(t.Ctx, emulation.SetUserAgentOverride(t.Header.UserAgent), page.HandleJavaScriptDialog(false))
		}
	}
}

// HTML 获取html
func (t *Tab) HTML() (html string, err error) {
	if err = chromedp.Run(t.Ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		b, err := network.GetResponseBody(t.LastRequestID).Do(ctx)
		if err != nil {
			logrus.Error(err)
			return err
		}
		html = string(b)
		return nil
	})); err != nil {
		return
	}

	err = chromedp.Run(t.Ctx,
		emulation.SetUserAgentOverride(t.Header.UserAgent),
		chromedp.EvaluateAsDevTools(`document.querySelector('html').outerHTML`, &html),
	)
	if err != nil {
		logrus.Error(err)
	}
	return html, err
}

func (t *Tab) FullScreenshot(quality ...int) ([]byte, error) {
	if quality == nil {
		quality = []int{85}
	}
	var res []byte
	err := chromedp.Run(t.Ctx,
		emulation.SetUserAgentOverride(t.Header.UserAgent),
		chromedp.FullScreenshot(&res, quality[0]),
	)
	return res, err
}

// FetchAllPictures 下载图片
func (t *Tab) FetchAllPictures() {
	imgs := make([]string, 0)
	for _, e := range t.Events {
		switch e.(type) {
		case *network.EventResponseReceived:
			ev := e.(*network.EventResponseReceived)
			if ev.Type == network.ResourceTypeImage && ev.LoaderID == t.LastLoaderID {
				imgs = append(imgs, ev.Response.URL)
			}
		}
	}

	eg, _ := errgroup.WithContext(context.Background())
	eg.SetLimit(10)
	// 下载图片
	for _, img := range imgs {
		i := img
		eg.TryGo(func() error {
			resp, err := http.Get(i)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			b, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				return err
			}

			// 取url最后一个i的文件名
			i = path.Base(i)

			err = ioutil.WriteFile(fmt.Sprintf("picture/%s", i), b, 0755)
			if err != nil {
				return err
			}
			return nil
		})
	}
	_ = eg.Wait()
}
