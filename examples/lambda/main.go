package ext

import (
	"context"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/simplified/lambda"
)

func main() {
	lambda.StartLambda(func(ctx context.Context, org *limacharlie.Organization, req limacharlie.Dict, idempotentKey string) (limacharlie.Dict, error) {
		return nil, nil
	})
}
