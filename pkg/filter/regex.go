package filter

import (
    "fmt"
    "regexp"
    "strings"

    jsoniter "github.com/json-iterator/go"
    "github.com/theblackturtle/ffuf/pkg/ffuf"
)

type RegexpFilter struct {
    Value    *regexp.Regexp
    valueRaw string
}

func NewRegexpFilter(value string) (ffuf.FilterProvider, error) {
    re, err := regexp.Compile(value)
    if err != nil {
        return &RegexpFilter{}, fmt.Errorf("Regexp filter or matcher (-fr / -mr): invalid value: %s", value)
    }
    return &RegexpFilter{Value: re, valueRaw: value}, nil
}

func (f *RegexpFilter) MarshalJSON() ([]byte, error) {
    return jsoniter.Marshal(&struct {
        Value string `json:"value"`
    }{
        Value: f.valueRaw,
    })
}

func (f *RegexpFilter) Filter(response *ffuf.Response) (bool, error) {
    matchheaders := ""
    for k, v := range response.Headers {
        for _, iv := range v {
            matchheaders += k + ": " + iv + "\r\n"
        }
    }
    matchdata := []byte(matchheaders)
    matchdata = append(matchdata, response.Data...)
    pattern := f.valueRaw
    for keyword, inputitem := range response.Request.Input {
        pattern = strings.Replace(pattern, keyword, regexp.QuoteMeta(string(inputitem)), -1)
    }
    matched, err := regexp.Match(pattern, matchdata)
    if err != nil {
        return false, nil
    }
    return matched, nil
}

func (f *RegexpFilter) Repr() string {
    return fmt.Sprintf("Regexp: %s", f.valueRaw)
}
