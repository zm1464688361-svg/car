package web

import (
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/zm1464688361-svg/car/internal/model"
)

// loadTemplates 解析全部页面模板（base 布局 + 页面体）。
func (s *Server) loadTemplates() error {
	funcs := template.FuncMap{
		"act":       model.ActLabel,
		"pub":       model.PubLabel,
		"batch":     model.BatchLabel,
		"roleLabel": model.Role.Label,
		"timefmt": func(s string) string {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				return s
			}
			return t.Local().Format("01-02 15:04:05")
		},
		"ptr": func(v *int64) string {
			if v == nil {
				return "-"
			}
			return template.HTMLEscaper(*v)
		},
		"seq": func(n int) []int {
			out := make([]int, n)
			for i := range out {
				out[i] = i
			}
			return out
		},
	}
	pages, err := templateFS.ReadDir("templates")
	if err != nil {
		return err
	}
	s.tpl = map[string]*template.Template{}
	for _, p := range pages {
		name := p.Name()
		if !strings.HasSuffix(name, ".html") || name == "base.html" {
			continue
		}
		t, err := template.New("base.html").Funcs(funcs).
			ParseFS(templateFS, "templates/base.html", "templates/"+name)
		if err != nil {
			return err
		}
		s.tpl[name] = t
	}
	return nil
}

// render 渲染页面；data 必须包含 User（可为 nil）、Flash、Nav。
type pageData struct {
	User      *model.User
	Flash     flash
	Title     string
	Timeout   string
	Data      map[string]any
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, page string, title string, data map[string]any, status int) {
	t, ok := s.tpl[page]
	if !ok {
		http.Error(w, "template missing: "+page, http.StatusInternalServerError)
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	pd := &pageData{
		User:    s.currentUser(r),
		Flash:   getFlash(r),
		Title:   title,
		Timeout: s.svc.Timeout.String(),
		Data:    data,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := t.ExecuteTemplate(w, "base.html", pd); err != nil {
		// 已开始写响应，仅记录
		_ = err
	}
}
