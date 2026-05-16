package handler

import "fmt"

func errMissingPostedID(kind string) error {
	return fmt.Errorf("%s post response did not include id", kind)
}
