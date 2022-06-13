// Copyright 2022 Juan Pablo Tosso
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package bodyprocessors

import (
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"os"
	"strings"

	"github.com/corazawaf/coraza/v2/types/variables"
)

type multipartBodyProcessor struct {
	collections *CollectionsMap
}

func (mbp *multipartBodyProcessor) Read(reader io.Reader, options Options) error {
	mimeType := options.Mime
	storagePath := options.StoragePath
	mediaType, params, err := mime.ParseMediaType(mimeType)
	if err != nil {
		log.Fatal(err)
	}
	if !strings.HasPrefix(mediaType, "multipart/") {
		return fmt.Errorf("not a multipart body")
	}
	mr := multipart.NewReader(reader, params["boundary"])
	totalSize := int64(0)
	filesNames := []string{}
	filesArgNames := []string{}
	fileList := []string{}
	fileSizes := []string{}
	postNames := []string{}
	postFields := map[string][]string{}
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// if is a file
		filename := originFileName(p)
		if filename != "" {
			temp, err := os.CreateTemp(storagePath, "crzmp*")
			if err != nil {
				return err
			}
			sz, err := io.Copy(temp, p)
			if err != nil {
				return err
			}
			totalSize += sz
			filesNames = append(filesNames, filename)
			fileList = append(fileList, temp.Name())
			fileSizes = append(fileSizes, fmt.Sprintf("%d", sz))
			filesArgNames = append(filesArgNames, p.FormName())
		} else {
			// if is a field
			data, err := io.ReadAll(p)
			if err != nil {
				return err
			}
			totalSize += int64(len(data))
			postNames = append(postNames, p.FormName())
			if _, ok := postFields[p.FormName()]; !ok {
				postFields[p.FormName()] = []string{}
			}
			postFields[p.FormName()] = append(postFields[p.FormName()], string(data))

		}
	}
	pn := map[string][]string{}
	for _, value := range postNames {
		pn[value] = []string{value}
	}
	mbp.collections = &CollectionsMap{
		variables.FilesNames: map[string][]string{
			"": filesArgNames,
		},
		variables.FilesTmpNames: map[string][]string{
			"": fileList,
		},
		variables.Files: map[string][]string{
			"": filesNames,
		},
		variables.FilesSizes: map[string][]string{
			"": fileSizes,
		},
		variables.ArgsPostNames: pn,
		variables.ArgsPost:      postFields,
		variables.Args:          postFields,
		variables.FilesCombinedSize: map[string][]string{
			"": {fmt.Sprintf("%d", totalSize)},
		},
	}

	return nil
}

func (mbp *multipartBodyProcessor) Collections() CollectionsMap {
	return *mbp.collections
}

func (mbp *multipartBodyProcessor) Find(expr string) (map[string][]string, error) {
	return nil, nil
}

func (mbp *multipartBodyProcessor) VariableHook() variables.RuleVariable {
	return variables.JSON
}

var (
	_ BodyProcessor = (*multipartBodyProcessor)(nil)
)

// OriginFileName returns the filename parameter of the Part's Content-Disposition header.
// This function is based on (multipart.Part).parseContentDisposition,
// See https://go.googlesource.com/go/+/refs/tags/go1.17.9/src/mime/multipart/multipart.go#87
// for the current implementation and also notice this function hasn't change since go1.4, as in
// https://go.googlesource.com/go/+/refs/tags/go1.4/src/mime/multipart/multipart.go#75
func originFileName(p *multipart.Part) string {
	v := p.Header.Get("Content-Disposition")
	_, dispositionParams, err := mime.ParseMediaType(v)
	if err != nil {
		return ""
	}

	return dispositionParams["filename"]
}
