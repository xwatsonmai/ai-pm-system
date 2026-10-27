// Package attachment validates uploaded data and extracts readable document text.
package attachment

import (
	"ai-pm-system/internal/domain"
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
	_ "golang.org/x/image/webp"
)

const MaxSize = 10 << 20
const MaxTotalSize = 20 << 20
const MaxCount = 6
const MaxTextRunes = 40000

func Prepare(project, name string, content []byte) (a domain.Attachment, err error) {
	// Invalid PDFs can panic inside the parser. Return an upload error instead
	// of taking down the request/server or pretending the file was understood.
	defer func() {
		if recover() != nil {
			err = errors.New("文件无法解析，请检查是否损坏或加密")
		}
	}()
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "." || name == "/" || len([]rune(name)) > 180 || strings.ContainsAny(name, "\x00\r\n") {
		return a, errors.New("文件名不合法")
	}
	if len(content) == 0 {
		return a, errors.New("不能添加空文件")
	}
	if len(content) > MaxSize {
		return a, errors.New("单个附件不能超过 10 MB")
	}
	a = domain.Attachment{ID: domain.ID("att_"), ProjectID: project, Name: name, Size: len(content), At: domain.Now(), Content: content}
	a.MIME = http.DetectContentType(content)
	switch a.MIME {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		cfg, _, e := image.DecodeConfig(bytes.NewReader(content))
		if e != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 40000000 {
			return a, errors.New("图片损坏或尺寸过大，请使用不超过 4000 万像素的图片")
		}
		if _, _, e := image.Decode(bytes.NewReader(content)); e != nil {
			return a, errors.New("图片内容损坏，请重新导出后添加")
		}
		a.Kind = "image"
		return a, nil
	}
	a.Kind = "document"
	switch strings.ToLower(path.Ext(name)) {
	case ".pdf":
		if !bytes.HasPrefix(content, []byte("%PDF-")) {
			return a, errors.New("文件不是有效的 PDF")
		}
		a.MIME = "application/pdf"
		a.Text, a.Truncated, err = pdfText(content)
	case ".docx":
		a.MIME = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
		a.Text, err = docxText(content)
	default:
		if !utf8.Valid(content) || bytes.ContainsRune(content, 0) {
			return a, errors.New("暂不支持此文件，请使用图片、PDF、DOCX 或 UTF-8 文本/代码文件")
		}
		for _, b := range content {
			if b < 32 && b != '\n' && b != '\r' && b != '\t' && b != '\f' {
				return a, errors.New("文件包含无法读取的二进制内容，请转换为 PDF、DOCX 或 UTF-8 文本")
			}
		}
		a.MIME = "text/plain; charset=utf-8"
		a.Text = strings.TrimPrefix(string(content), "\ufeff")
	}
	if err != nil {
		return a, fmt.Errorf("无法读取 %s：%w", name, err)
	}
	if strings.TrimSpace(a.Text) == "" {
		return a, errors.New("文件没有可读取的文字；扫描版 PDF 请转换为图片后添加")
	}
	text := []rune(a.Text)
	if len(text) > MaxTextRunes {
		a.Text = string(text[:MaxTextRunes])
		a.Truncated = true
	}
	return a, nil
}

func pdfText(content []byte) (string, bool, error) {
	r, err := pdf.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", false, err
	}
	var out strings.Builder
	pages := min(r.NumPage(), 50)
	truncated := pages < r.NumPage()
	for n := 1; n <= pages; n++ {
		page := r.Page(n)
		text, err := page.GetPlainText(nil)
		if err != nil {
			return "", false, err
		}
		fmt.Fprintf(&out, "\n%s\n", text)
		if utf8.RuneCountInString(out.String()) > MaxTextRunes {
			truncated = true
			break
		}
	}
	return out.String(), truncated, nil
}

func docxText(content []byte) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", err
	}
	for _, f := range r.File {
		if f.Name != "word/document.xml" {
			continue
		}
		if f.UncompressedSize64 > 4<<20 {
			return "", errors.New("文档正文过大，请拆分后添加")
		}
		reader, err := f.Open()
		if err != nil {
			return "", err
		}
		defer reader.Close()
		d := xml.NewDecoder(io.LimitReader(reader, 4<<20))
		var out strings.Builder
		for {
			tok, err := d.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return "", err
			}
			switch t := tok.(type) {
			case xml.StartElement:
				if t.Name.Local == "t" {
					var text string
					if err = d.DecodeElement(&text, &t); err != nil {
						return "", err
					}
					out.WriteString(text)
				}
				if t.Name.Local == "tab" {
					out.WriteByte('\t')
				}
				if t.Name.Local == "br" {
					out.WriteByte('\n')
				}
			case xml.EndElement:
				if t.Name.Local == "p" {
					out.WriteByte('\n')
				}
			}
		}
		return out.String(), nil
	}
	return "", errors.New("DOCX 缺少文档正文")
}
