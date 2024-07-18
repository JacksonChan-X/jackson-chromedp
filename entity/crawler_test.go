package entity

import (
	_ "embed"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/sirupsen/logrus"
)

//go:embed test.txt
var urls string

func TestCrawler(t *testing.T) {
	b := NewRemote("http://127.0.0.1:9222", WithCapcity(10))
	defer b.Close()

	wg := sync.WaitGroup{}
	ch := make(chan struct{}, 20)
	for i, url := range strings.Split(urls, "\n") {
		if len(strings.TrimSpace(url)) == 0 {
			continue
		}

		ch <- struct{}{}
		wg.Add(1)

		u := url
		ii := i
		go func() error {
			defer func() {
				wg.Done()
				<-ch
			}()

			tab, err := b.NewTab(u, WithUserAgent(UserAgent))
			if err != nil {
				return err
			}
			defer tab.Close()

			err = chromedp.Run(tab.Ctx, tab.BuildHooks()...)
			if err != nil {
				return err
			}
			tab.waitNetworkAlmostIdle(time.Second * 30)
			logrus.Info(ii, " ", u)

			picture, err := tab.FullScreenshot(85)
			// 保存picture
			if err != nil {
				return err
			}
			logrus.Info(ii, " ", u)
			os.WriteFile("./"+strconv.Itoa(ii)+".jpg", picture, 0644)

			return nil
		}()
	}

	wg.Wait()
}

func TestExec(t *testing.T) {
	b := NewExec(false)
	defer b.Close()

	tab, err := b.NewTab("http://personalevolvement.com")
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Close()

	err = chromedp.Run(tab.Ctx, tab.BuildHooks()...)
	if err != nil {
		t.Fatal(err)
	}
	tab.waitNetworkAlmostIdle(time.Second * 30)

	tab.FetchRedirectNodes()

	html, err := tab.HTML()
	if err != nil {
		t.Fatal(err)
	}
	t.Log(len(html))
}
