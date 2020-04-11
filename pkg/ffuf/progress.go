package ffuf

import (
    "time"
)

type Progress struct {
    StartedAt  time.Time
    ReqCount   int
    ReqTotal   int
    QueuePos   int
    QueueTotal int
    ErrorCount int
}
