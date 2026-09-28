package localenv

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

func Load(ctx context.Context) error {
	for _, name := range []string{".env", ".env.example"} {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := godotenv.Load(name); err != nil {
			if name == ".env" && errors.Is(err, os.ErrNotExist) {
				continue
			}

			return fmt.Errorf("cannot load %s: check file access and dotenv syntax", name)
		}
	}
	return nil
}
