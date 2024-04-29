package entity

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/sirupsen/logrus"
)

const (
	UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/89.207.132.170 Safari/537.36"
)

type Browser struct {
	Ctx    context.Context
	Cancel context.CancelFunc

	Capcity int
	RWLock  sync.RWMutex
	Tabs    sync.Map
	Pool    chan struct{} // 定义有缓冲通道
}

type BrowserOption func(*Browser)

func WithCapcity(capcity int) BrowserOption {
	return func(b *Browser) {
		b.Capcity = capcity
	}
}

func NewRemote(url string, bOpts ...BrowserOption) *Browser {
	ctx, cancel := chromedp.NewRemoteAllocator(context.Background(), url)
	b := &Browser{
		Ctx:    ctx,
		Cancel: cancel,
		Tabs:   sync.Map{},
	}

	for _, opt := range bOpts { // browser配置项
		opt(b)
	}

	// 创建tab有缓冲通道
	if b.Capcity <= 0 || b.Capcity > 50 {
		b.Capcity = 10
	}
	b.Pool = make(chan struct{}, b.Capcity)

	return b
}

// NewExec 新建浏览器
// headless: true 隐藏浏览器
func NewExec(headless bool, bOpts ...BrowserOption) *Browser {
	opts := []chromedp.ExecAllocatorOption{
		chromedp.UserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36"),
		chromedp.NoFirstRun,
		chromedp.NoSandbox,
		chromedp.NoDefaultBrowserCheck,
		chromedp.IgnoreCertErrors,
		chromedp.Flag("enable-automation", false), // disable automation
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
	}
	opts = append(opts, chromedp.DefaultExecAllocatorOptions[3:]...) // chromedp配置项
	if headless {
		opts = append(opts, chromedp.Headless)
	}

	ctx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	b := &Browser{
		Ctx:    ctx,
		Cancel: cancel,
		Tabs:   sync.Map{},
	}
	for _, opt := range bOpts { // browser配置项
		opt(b)
	}

	// 创建tab有缓冲通道
	if b.Capcity <= 0 || b.Capcity > 50 {
		b.Pool = make(chan struct{}, 25)
	} else {
		b.Pool = make(chan struct{}, b.Capcity)
	}

	b.Ctx, _ = chromedp.NewContext(b.Ctx)
	err := chromedp.Run(b.Ctx)
	if err != nil {
		logrus.Error(err)
	}
	return b
}

// Close 关闭所有资源
func (b *Browser) Close() {
	b.RWLock.Lock()
	defer b.RWLock.Unlock()

	b.Tabs.Range(func(key, value interface{}) bool {
		tab := value.(*Tab)
		tab.Cancel()
		return true
	})

	chromedp.Cancel(b.Ctx)
	b.Cancel()
}

// NewTab 新建tab
// 开协程，防止阻塞
func (b *Browser) NewTab(url string, opts ...TabOption) (*Tab, error) {
	timer := time.NewTimer(time.Minute) //	超时时间
	defer timer.Stop()

	select {
	case b.Pool <- struct{}{}: //	通道满了就阻塞
	case <-timer.C:
		logrus.Warn("tab pool timeout")
		return nil, errors.New("tab pool timeout")
	}

	ctx, cancel := chromedp.NewContext(b.Ctx)
	tab := &Tab{
		Ctx:           ctx,
		Cancel:        cancel,
		Events:        make([]any, 0),
		Parent:        b,
		FirstResponse: new(network.Response),
		LastResponse:  new(network.Response),
		Header:        Header{},
		URL:           url,
	}
	for _, opt := range opts {
		opt(tab)
	}

	b.RWLock.Lock()
	defer b.RWLock.Unlock()
	b.Tabs.Store(tab, tab) //TODO:

	return tab, nil
}
