package handlers

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
)

func copyDownloadSnapshot(dst io.Writer, src io.Reader, size int64) error {
	_, err := io.CopyN(dst, src, size)
	return err
}

func uploadedFilePart(r *http.Request) (*multipart.Part, error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, fmt.Errorf("failed to parse multipart form: %w", err)
	}

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return nil, fmt.Errorf("file field is required")
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read multipart form: %w", err)
		}
		if part.FormName() == "file" {
			return part, nil
		}
		part.Close()
	}
}
