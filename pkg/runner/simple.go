package runner

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ffuf/ffuf/pkg/ffuf"
	"github.com/valyala/fasthttp"
)

//Download results < 5MB
const MAX_DOWNLOAD_SIZE = 5242880

type SimpleRunner struct {
	config *ffuf.Config
	client *fasthttp.Client
}

func NewSimpleRunner(conf *ffuf.Config, replay bool) ffuf.RunnerProvider {
	var simplerunner SimpleRunner

	simplerunner.config = conf
	simplerunner.client = &fasthttp.Client{
		NoDefaultUserAgentHeader: true,
		Dial: func(addr string) (net.Conn, error) {
			return fasthttp.DialTimeout(addr, 15*time.Second)
		},
		TLSConfig: &tls.Config{
			InsecureSkipVerify: true,
			Renegotiation:      tls.RenegotiateOnceAsClient, // For "local error: tls: no renegotiation"
		},
	}

	return &simplerunner
}

func (r *SimpleRunner) Prepare(input map[string][]byte) (ffuf.Request, error) {
	req := ffuf.NewRequest(r.config)

	req.Headers = r.config.Headers
	req.Url = r.config.Url
	req.Method = r.config.Method
	req.Data = []byte(r.config.Data)

	for keyword, inputitem := range input {
		req.Method = strings.Replace(req.Method, keyword, string(inputitem), -1)
		headers := make(map[string]string, 0)
		for h, v := range req.Headers {
			var CanonicalHeader = textproto.CanonicalMIMEHeaderKey(strings.Replace(h, keyword, string(inputitem), -1))
			headers[CanonicalHeader] = strings.Replace(v, keyword, string(inputitem), -1)
		}
		req.Headers = headers
		req.Url = strings.Replace(req.Url, keyword, string(inputitem), -1)
		req.Data = []byte(strings.Replace(string(req.Data), keyword, string(inputitem), -1))
	}

	req.Input = input
	return req, nil
}

/*
TODO: fix DumpRequestOut, DumpResponse, set proxy
NOTE: Fasthttp's DoTimeout not have option to redirect, so need to create new function for this job
*/

func (r *SimpleRunner) Execute(req *ffuf.Request) (ffuf.Response, error) {
	var err error
	httpreq := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(httpreq)
	httpreq.SetRequestURI(req.Url)
	httpreq.Header.SetMethod(req.Method)
	httpreq.SetBody(req.Data)

	// set default User-Agent header if not present
	if _, ok := req.Headers["User-Agent"]; !ok {
		httpreq.Header.Set("User-Agent", fmt.Sprintf("%s v%s", "Fuzz Faster U Fool", ffuf.VERSION))
	}

	// Handle Go http.Request special cases
	if _, ok := req.Headers["Host"]; ok {
		httpreq.SetHost(req.Headers["Host"])
	}
	for key, value := range req.Headers {
		httpreq.Header.Set(key, value)
	}

	httpresp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(httpresp)

	err = r.client.DoTimeout(httpreq, httpresp, time.Duration(r.config.Timeout)*time.Second)
	if err != nil {
		return ffuf.Response{}, err
	}

	resp := ffuf.NewResponse(httpresp, req)

	// Check if we should download the resource or not

	size, err := strconv.Atoi(string(httpresp.Header.Peek(fasthttp.HeaderContentLength)))
	if err == nil {
		resp.ContentLength = int64(size)
		if (r.config.IgnoreBody) || (size > MAX_DOWNLOAD_SIZE) {
			resp.Cancelled = true
			return resp, nil
		}
	}

	contentEncoding := string(httpresp.Header.Peek(fasthttp.HeaderContentEncoding))
	var respbody []byte
	if contentEncoding != "" {
		if contentEncoding == "gzip" {
			respbody, err = httpresp.BodyGunzip()
			if err != nil {
				return ffuf.Response{}, err
			}
		} else if contentEncoding == "deflate" {
			respbody, err = httpresp.BodyInflate()
			if err != nil {
				return ffuf.Response{}, err
			}
		} else {
			return ffuf.Response{}, err
		}
	} else {
		respbody = httpresp.Body()
	}

	resp.ContentLength = int64(utf8.RuneCountInString(string(respbody)))
	resp.Data = respbody

	wordsSize := len(strings.Split(string(resp.Data), " "))
	linesSize := len(strings.Split(string(resp.Data), "\n"))
	resp.ContentWords = int64(wordsSize)
	resp.ContentLines = int64(linesSize)

	return resp, nil
}
