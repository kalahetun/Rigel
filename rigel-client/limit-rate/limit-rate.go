package limit_rate

import (
	"context"
	"golang.org/x/time/rate"
	"io"
)

type RateLimitedReader struct {
	r       io.Reader
	limiter *rate.Limiter
	ctx     context.Context
}

func (rr *RateLimitedReader) Read(p []byte) (int, error) {
	if rr.limiter != nil {
		// 按本次要读的字节数限流
		if err := rr.limiter.WaitN(rr.ctx, len(p)); err != nil {
			return 0, err
		}
	}
	return rr.r.Read(p)
}

func NewRateLimitedReader(
	ctx context.Context,
	r io.Reader,
	limiter *rate.Limiter,
) *RateLimitedReader {
	if ctx == nil {
		ctx = context.Background()
	}

	return &RateLimitedReader{
		r:       r,
		limiter: limiter,
		ctx:     ctx,
	}
}
