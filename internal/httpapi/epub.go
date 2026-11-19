package httpapi

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const epubJSVersion = "reader-inject-javascript-1.1.0"

// epubInjectJavascript is the exact JS payload injected into EPUB XHTML chapters
// (mirrors com.htmake.reader.config.BookConfig in the original Kotlin source).
const epubInjectJavascript = `    //<![CDATA[
    // ` + epubJSVersion + `
    if (!window.reader_inited) {
        function reader_notify(event, data, id) {
            if (window.self !== window.top) {
                window.top.postMessage(JSON.stringify({
                    id: id,
                    event: event,
                    data: data
                }), '*');
            }
        }

        var reader_style_dom = document.createElement('style');
        var head = document.head || document.getElementsByTagName('head')[0];
        head.appendChild(reader_style_dom);

        function reader_setStyle(style) {
            reader_style_dom.innerText = style;
            reader_notifyHeight();
            setTimeout(reader_notifyHeight, 100);
        }

        function reader_notifyHeight() {
            reader_notify("setHeight", document.documentElement.scrollHeight || document.body.scrollHeight)
        }

        function reader_listenFromParent(event) {
            reader_notify('received', {
                data: event.data
            })
            let data;
            try {
                data = typeof event.data === 'string' ? JSON.parse(event.data) : event.data;
            } catch (error) {
                return;
            }

            if (!data) {
                return;
            }
            reader_notify("data ", data);
            if (data.event === 'setStyle') {
                reader_setStyle(data.style);
            } else if (data.event === 'execute') {
                eval(data.script);
            } else if (data.id) {
                if (window.nativeCallback[data.id]) {
                    window.nativeCallback[data.id](data);
                    delete window.nativeCallback[data.id]
                }
            }
        }


        function reader_getLinkElement(element) {
            if (!element || !element.nodeName) {
                return false;
            }
            if (element.nodeName.toLowerCase() === "a") {
                return element;
            }
            return reader_getLinkElement(element.parentNode)
        }

        function reader_getImageElement(element) {
            if (!element || !element.nodeName) {
                return false;
            }
            if (element.nodeName.toLowerCase() === "img") {
                return element;
            }
        }

        window.document.addEventListener('message', reader_listenFromParent);
        window.addEventListener('message', reader_listenFromParent);
        window.addEventListener('load', function() {
            reader_notifyHeight();
            reader_notify("load", window.location.href);
        });
        window.addEventListener('resize', reader_notifyHeight);
        document.addEventListener('DOMNodeInserted', reader_notifyHeight, false);
        document.addEventListener('click', function(event) {
            var linkElement = reader_getLinkElement(event.target)
            var imageElement = reader_getImageElement(event.target)
            if (linkElement) {
                if (linkElement.pathname === window.location.pathname) {
                    var hashElement = document.querySelector(linkElement.hash)
                    if (hashElement) {
                        reader_notify("clickHash", hashElement.getBoundingClientRect())
                    }
                } else {
                    reader_notify("clickA", event.target.href);
                }
            } else if (imageElement) {
                var imgList = document.querySelectorAll("img");
                if (imgList.length) {
                    var imgUrlList = [];
                    var index = 0;
                    for (let i = 0; i < imgList.length; i++) {
                        imgUrlList.push(imgList[i].src);
                        if (imgList[i] === imageElement) {
                            index = i;
                        }
                    }
                    reader_notify("previewImageList", {
                        imageList: imgUrlList,
                        imageIndex: index
                    });
                }
            } else {
                reader_notify("click", {
                    target: event.target.nodeName,
                    clientX: event.clientX,
                    clientY: event.clientY
                });
            }
        });
        window.addEventListener("keydown", function(event) {
            event.preventDefault();
            event.stopPropagation();
            reader_notify("keydown", {
                key: event.key,
                keyCode: event.keyCode
            });
        });
        reader_notify("inited");

        window.reader_inited = true;
    }
    //]]>
    `

// epubInjectMu serialises writes to the same EPUB XHTML file so concurrent
// readers of the same chapter don't race. Different files don't contend.
var epubInjectMu sync.Mutex

func injectJavascriptToEpubChapter(filePath string) {
	epubInjectMu.Lock()
	defer epubInjectMu.Unlock()

	data, err := os.ReadFile(filePath)
	if err != nil {
		return
	}
	content := string(data)
	if strings.Contains(content, epubJSVersion) {
		return
	}
	newContent := strings.Replace(content, "<head>", `<head><script type="text/javascript">`+epubInjectJavascript+`</script>`, 1)
	if newContent == content {
		// Some EPUBs use case-variants like <HEAD> — do the same case-insensitively.
		newContent = strings.Replace(strings.ToLower(content), "<head>", `<head><script type="text/javascript">`+epubInjectJavascript+`</script>`, 1)
		if newContent == strings.ToLower(content) {
			return
		}
	}
	if err := os.WriteFile(filePath, []byte(newContent), 0o644); err != nil {
		logger.Error("epub inject: write failed", "file", filePath, "error", err)
	}
}

// epubHandler returns an http.Handler that serves the EPUB data directory
// and lazily injects reader-inject-javascript into every .html / .xhtml chapter.
func epubHandler(dataDir string) http.Handler {
	base := http.StripPrefix("/epub/", http.FileServer(http.Dir(dataDir)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := url.PathUnescape(r.URL.Path)
		if err != nil {
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		// Strip the "/epub/" prefix so we get the relative path into dataDir,
		// mirroring what http.StripPrefix above will do for the underlying handler.
		p = strings.TrimPrefix(p, "/epub")
		p = strings.TrimPrefix(p, "/")
		low := strings.ToLower(p)
		if strings.HasSuffix(low, ".html") || strings.HasSuffix(low, ".xhtml") {
			full := filepath.Join(dataDir, filepath.FromSlash(p))
			if st, err := os.Stat(full); err == nil && !st.IsDir() {
				injectJavascriptToEpubChapter(full)
			}
		}
		base.ServeHTTP(w, r)
	})
}
