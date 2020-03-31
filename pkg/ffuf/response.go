package ffuf

import (
	"github.com/valyala/fasthttp"
	"net/url"
	"strings"
)

// Response struct holds the meaningful data returned from request and is meant for passing to filters
type Response struct {
	StatusCode    int64
	Headers       map[string][]string
	Data          []byte
	ContentLength int64
	ContentWords  int64
	ContentLines  int64
	Cancelled     bool
	Request       *Request
	Raw           string
	ResultFile    string
}

// GetRedirectLocation returns the redirect location for a 3xx redirect HTTP response
func (resp *Response) GetRedirectLocation(absolute bool) string {

	redirectLocation := ""
	if resp.StatusCode >= 300 && resp.StatusCode <= 399 {
		redirectLocation = resp.Headers["Location"][0]
	}

	if absolute {
		redirectUrl, err := url.Parse(redirectLocation)
		if err != nil {
			return redirectLocation
		}
		baseUrl, err := url.Parse(resp.Request.Url)
		if err != nil {
			return redirectLocation
		}
		redirectLocation = baseUrl.ResolveReference(redirectUrl).String()
	}

	return redirectLocation
}

func NewResponse(httpresp *fasthttp.Response, req *Request) Response {
	headers := map[string][]string{}
	for _, header := range strings.Split(string(httpresp.Header.Header()), "\n") {
		args := strings.SplitN(header, ":", 2)
		if len(args) == 2 {
			headers[strings.TrimSpace(args[0])] = []string{strings.TrimSpace(args[1])}
		}
	}

	var resp Response
	resp.Request = req
	resp.StatusCode = int64(httpresp.StatusCode())
	resp.Headers = headers
	resp.Cancelled = false
	resp.Raw = ""
	resp.ResultFile = ""
	return resp
}
