package service

import "testing"

func TestValidate(t *testing.T) {
	p := 1500
	ok := TemplateInput{ID: "enzo", Name: "Enzo", Prompt: "x",
		SlotLabelsRu: []string{"a"}, SlotLabelsKz: []string{"b"}, Price1080: &p, Active: true}
	if tpl, err := ok.validate(); err != nil || tpl.RefPhotos != 1 {
		t.Fatalf("валидный шаблон отвергнут: %v", err)
	}

	bad := map[string]func(*TemplateInput){
		"id с путём":        func(in *TemplateInput) { in.ID = "../x" },
		"нет подписей":      func(in *TemplateInput) { in.SlotLabelsRu, in.SlotLabelsKz = nil, nil },
		"kz не совпадает":   func(in *TemplateInput) { in.SlotLabelsKz = []string{"a", "b"} },
		"пустой prompt":     func(in *TemplateInput) { in.Prompt = " " },
		"в каталог без цен": func(in *TemplateInput) { in.Price1080 = nil },
		"нулевая цена":      func(in *TemplateInput) { z := 0; in.Price480 = &z },
	}
	for name, mut := range bad {
		in := ok
		mut(&in)
		if _, err := in.validate(); err == nil {
			t.Errorf("%s: ожидали ошибку", name)
		}
	}
}
