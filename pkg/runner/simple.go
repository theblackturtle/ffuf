package runner

import (
    "crypto/tls"
    "errors"
    "net"
    "net/textproto"
    "strconv"
    "strings"
    "time"
    "unicode/utf8"

    "github.com/theblackturtle/ffuf/pkg/ffuf"
    "github.com/valyala/fasthttp"
)

// Download results < 5MB
const (
    MAX_DOWNLOAD_SIZE = 5242880
    MaxRedirectTimes  = 16
)

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
            return fasthttp.Dial(addr)
        },
        ReadBufferSize:      1024*48,
        WriteBufferSize:     1024*48,
        TLSConfig: &tls.Config{
            InsecureSkipVerify: true,
            Renegotiation:      tls.RenegotiateOnceAsClient, // For "local error: tls: no renegotiation"
        },
        MaxResponseBodySize: MAX_DOWNLOAD_SIZE,
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
*/

func (r *SimpleRunner) Execute(req *ffuf.Request) (ffuf.Response, error) {
    var err error
    httpreq := fasthttp.AcquireRequest()
    defer fasthttp.ReleaseRequest(httpreq)
    httpreq.SetRequestURI(req.Url)
    httpreq.Header.SetMethod(req.Method)
    httpreq.SetBody(req.Data)
    httpreq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
    httpreq.Header.Set("Accept-Language", "en-US,en;q=0.8")

    for key, value := range req.Headers {
        httpreq.Header.Set(key, value)
    }
    // set default User-Agent header if not present
    if _, ok := req.Headers["User-Agent"]; !ok {
        httpreq.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_4) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/80.0.3987.149 Safari/537.36")
    }

    // Handle Go http.Request special cases
    if _, ok := req.Headers["Host"]; ok {
        httpreq.SetHost(req.Headers["Host"])
    }

    httpresp := fasthttp.AcquireResponse()
    defer fasthttp.ReleaseResponse(httpresp)

    redirectTimes := 0
    for {
        err = r.client.DoTimeout(httpreq, httpresp, time.Duration(r.config.Timeout)*time.Second)
        if err != nil {
            if errors.Is(err, fasthttp.ErrBodyTooLarge) {
                resp := ffuf.NewResponse(httpresp, req)
                resp.Cancelled = true
                return resp, nil
            } else {
                return ffuf.Response{}, err
            }
        }
        if fasthttp.StatusCodeIsRedirect(httpresp.StatusCode()) && r.config.FollowRedirects {
            redirectTimes++
            if redirectTimes > MaxRedirectTimes {
                return ffuf.Response{}, errors.New("too many redirects")
            }

            nextLocation := httpresp.Header.Peek(fasthttp.HeaderLocation)
            if len(nextLocation) == 0 {
                return ffuf.Response{}, errors.New("location header not found")
            }
            req.Url = string(nextLocation)
            httpreq.SetRequestURI(getRedirectURL(req.Url, nextLocation))
            continue
        }
        break
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

func getRedirectURL(baseURL string, location []byte) string {
    u := fasthttp.AcquireURI()
    u.Update(baseURL)
    u.UpdateBytes(location)
    redirectURL := u.String()
    fasthttp.ReleaseURI(u)
    return redirectURL
}
