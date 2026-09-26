package llm

import "testing"

// Only PDFs and common images go to Claude; other attachments are skipped.
func TestFileBlocksReadableOnly(t *testing.T) {
	for media, want := range map[string]bool{"application/pdf": true, "image/png": true, "text/plain": false, "application/zip": false} {
		if Readable(media) != want {
			t.Errorf("Readable(%s) = %v", media, !want)
		}
		if _, ok := fileBlock(File{Media: media, Content: []byte("x")}); ok != want {
			t.Errorf("fileBlock(%s) = %v", media, ok)
		}
	}
}
