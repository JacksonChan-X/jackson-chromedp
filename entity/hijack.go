package entity

import (
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
)

type HijackRequestFunc func(*fetch.EventRequestPaused) *fetch.ContinueRequestParams

func BlockResourceType(typ network.ResourceType) HijackRequestFunc {
	return func(ev *fetch.EventRequestPaused) *fetch.ContinueRequestParams {
		if ev.ResourceType == typ {
			return nil
		}
		return fetch.ContinueRequest(ev.RequestID)
	}
}

func BlockEverything() HijackRequestFunc {
	return func(ev *fetch.EventRequestPaused) *fetch.ContinueRequestParams {
		return nil
	}
}
