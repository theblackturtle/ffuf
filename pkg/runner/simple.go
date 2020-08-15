package runner

import (
    "bytes"
    "crypto/tls"
    "errors"
    "fmt"
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
            return fasthttp.DialDualStackTimeout(addr, 30*time.Second)
        },
        ReadBufferSize:  48 << 10,
        WriteBufferSize: 48 << 10,
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
TODO: Implement proxy
*/

func (r *SimpleRunner) Execute(req *ffuf.Request) (ffuf.Response, error) {
    var err error
    fasthttpReq := fasthttp.AcquireRequest()
    defer fasthttp.ReleaseRequest(fasthttpReq)
    fasthttpReq.Header.SetRequestURI(req.Url)
    fasthttpReq.Header.SetMethod(req.Method)
    fasthttpReq.SetBody(req.Data)
    fasthttpReq.Header.Set("Accept", "*/*")
    fasthttpReq.Header.Set("Accept-Language", "en-US,en;q=0.8")

    for key, value := range req.Headers {
        fasthttpReq.Header.Set(key, value)
    }
    // set default User-Agent header if not present
    if _, ok := req.Headers["User-Agent"]; !ok {
        fasthttpReq.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_3) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/80.0.3987.132 Safari/537.36")
    }

    // Handle Go http.Request special cases
    if _, ok := req.Headers["Host"]; ok {
        fasthttpReq.SetHost(req.Headers["Host"])
    }
    req.Host = string(fasthttpReq.Host())

    fasthttpResp := fasthttp.AcquireResponse()
    defer fasthttp.ReleaseResponse(fasthttpResp)

    redirectTimes := 0
    for {
        err = r.client.DoTimeout(fasthttpReq, fasthttpResp, time.Duration(r.config.Timeout)*time.Second)
        if err != nil {
            if errors.Is(err, fasthttp.ErrBodyTooLarge) {
                resp := ffuf.NewResponse(fasthttpResp, req)
                resp.Cancelled = true
                return resp, nil
            } else {
                return ffuf.Response{}, err
            }
        }
        if fasthttp.StatusCodeIsRedirect(fasthttpResp.StatusCode()) && r.config.FollowRedirects {
            redirectTimes++
            if redirectTimes > MaxRedirectTimes {
                return ffuf.Response{}, errors.New("too many redirects")
            }

            nextLocation := fasthttpResp.Header.Peek(fasthttp.HeaderLocation)
            if len(nextLocation) == 0 {
                return ffuf.Response{}, errors.New("location header not found")
            }
            redirectUrl := getRedirectURL(req.Url, nextLocation)
            req.Url = redirectUrl
            fasthttpReq.Header.SetRequestURI(redirectUrl)
            continue
        }
        break
    }
    resp := ffuf.NewResponse(fasthttpResp, req)
    // Check if we should download the resource or not
    size, err := strconv.Atoi(string(fasthttpResp.Header.Peek(fasthttp.HeaderContentLength)))
    if err == nil {
        resp.ContentLength = int64(size)
        if (r.config.IgnoreBody) || (size > MAX_DOWNLOAD_SIZE) {
            resp.Cancelled = true
            return resp, nil
        }
    }

    contentEncoding := string(fasthttpResp.Header.Peek(fasthttp.HeaderContentEncoding))
    var respBody []byte

    switch contentEncoding {
    case "gzip":
        respBody, err = fasthttpResp.BodyGunzip()
        if err != nil {
            return ffuf.Response{}, err
        }
    case "deflate":
        respBody, err = fasthttpResp.BodyInflate()
        if err != nil {
            return ffuf.Response{}, err
        }
    default:
        respBody = fasthttpResp.Body()
    }

    resp.ContentLength = int64(utf8.RuneCountInString(string(respBody)))
    resp.Data = respBody

    resp.ContentWords = int64(len(strings.Split(string(resp.Data), " ")))
    resp.ContentLines = int64(len(strings.Split(string(resp.Data), "\n")))

    statusFormat := fmt.Sprintf("Status: %d, Size: %d, Words: %d, Lines: %d", resp.StatusCode, resp.ContentLength, resp.ContentWords, resp.ContentLines)
    if len(r.config.OutputDirectory) > 0 {
        httpreq := dumpRequest(fasthttpReq, statusFormat)
        resp.Request.Raw = httpreq
        resp.Raw = dumpResponse(fasthttpResp, string(respBody))
    }

    return resp, nil
}

func dumpRequest(req *fasthttp.Request, statusFormat string) string {
    buf := &bytes.Buffer{}
    buf.WriteString(req.URI().String())
    buf.WriteString("\n\n")
    buf.WriteString(statusFormat)
    buf.WriteString("\n\n")
    req.Header.VisitAll(func(key, value []byte) {
        buf.WriteString(fmt.Sprintf("> %s: %s\n", string(key), string(value)))
    })
    return buf.String()
}

func dumpResponse(resp *fasthttp.Response, body string) string {
    buf := &bytes.Buffer{}
    buf.WriteString(fmt.Sprintf("< HTTP/1.1 %d\n", resp.StatusCode()))
    resp.Header.VisitAll(func(key, value []byte) {
        buf.WriteString(fmt.Sprintf("< %s: %s\n", string(key), string(value)))
    })
    buf.WriteString("\n")
    buf.WriteString(body)
    return buf.String()
}

func getRedirectURL(baseURL string, location []byte) string {
    u := fasthttp.AcquireURI()
    u.Update(baseURL)
    u.UpdateBytes(location)
    redirectURL := u.String()
    fasthttp.ReleaseURI(u)
    return redirectURL
}
