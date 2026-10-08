package desktop

import (
	"io"
	"mime/multipart"
)

// multipartWriter is a small wrapper around multipart.Writer so callers can
// add files and fields with error handling in one place.
type multipartWriter struct {
	w *multipart.Writer
}

func newMultipart(dst io.Writer) *multipartWriter {
	return &multipartWriter{w: multipart.NewWriter(dst)}
}

func (m *multipartWriter) File(field, name string, data []byte) error {
	fw, err := m.w.CreateFormFile(field, name)
	if err != nil {
		return err
	}
	_, err = fw.Write(data)
	return err
}

func (m *multipartWriter) Field(name, value string) error {
	return m.w.WriteField(name, value)
}

func (m *multipartWriter) Close() error { return m.w.Close() }

func (m *multipartWriter) ContentType() string { return m.w.FormDataContentType() }
