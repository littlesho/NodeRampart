// SPDX-License-Identifier: MIT

package evidence

import (
	"context"
	"errors"
	"unicode/utf8"
)

// PublishTextfile replaces only a caller-owned 0600/0640 regular single-link
// file in a trusted directory. Evidence exports still require new files. A
// private first export is 0600; an operator-selected read group is preserved.
func PublishTextfile(ctx context.Context, path string, data []byte) error {
	if len(data) == 0 || len(data) > 16<<10 || !utf8.Valid(data) {
		return errors.New("textfile must contain 1..16384 UTF-8 bytes")
	}
	output, err := newOutputWithReplacement(path, true)
	if err != nil {
		return err
	}
	defer output.close()
	if _, err := output.file.Write(data); err != nil {
		return errors.New("textfile could not be written")
	}
	return output.publishContext(ctx)
}
