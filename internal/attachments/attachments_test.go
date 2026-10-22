package attachments

import "testing"

func TestValidateFilename拒绝非UTF8(t *testing.T) {
	if err := ValidateFilename("\xff\xfe.txt"); err != ErrInvalidFilename {
		t.Fatalf("ValidateFilename(\\xff\\xfe.txt) = %v, want ErrInvalidFilename", err)
	}
}
