package config

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Lector mínimo de TOML plano.
//
// El charter §6.2 exige TOML para la configuración de aplicación; ADR-0013 y
// P8 exigen justificar cada dependencia. La configuración de este servicio son
// seis claves: traer un analizador completo de TOML para eso no se sostiene.
//
// Se admite el subconjunto que se usa, y NADA más:
//
//	# comentario
//	[seccion]
//	clave = "texto"
//	clave = 8080
//	clave = true
//
// Cualquier construcción fuera de ese subconjunto —tablas anidadas, arrays,
// cadenas multilínea— produce un error explícito en lugar de ignorarse en
// silencio (P5). Si algún día hace falta más, se trae la dependencia y se
// justifica en un ADR; lo que no se hace es adivinar.
func leerTOML(r io.Reader) (map[string]string, error) {
	valores := make(map[string]string)
	seccion := ""
	sc := bufio.NewScanner(r)
	linea := 0
	for sc.Scan() {
		linea++
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if strings.HasPrefix(l, "[") {
			if !strings.HasSuffix(l, "]") {
				return nil, fmt.Errorf("línea %d: sección mal formada: %q", linea, l)
			}
			seccion = strings.TrimSpace(l[1 : len(l)-1])
			if seccion == "" || strings.ContainsAny(seccion, "[].") {
				return nil, fmt.Errorf("línea %d: solo se admiten secciones simples, no %q", linea, seccion)
			}
			continue
		}
		clave, valor, ok := strings.Cut(l, "=")
		if !ok {
			return nil, fmt.Errorf("línea %d: se esperaba «clave = valor», hay %q", linea, l)
		}
		clave = strings.TrimSpace(clave)
		valor = strings.TrimSpace(valor)
		if i := strings.Index(valor, " #"); i >= 0 {
			valor = strings.TrimSpace(valor[:i]) // comentario al final
		}
		if clave == "" {
			return nil, fmt.Errorf("línea %d: clave vacía", linea)
		}
		if strings.HasPrefix(valor, "[") || strings.HasPrefix(valor, "{") {
			return nil, fmt.Errorf("línea %d: arrays y tablas en línea no están admitidos", linea)
		}
		if len(valor) >= 2 && valor[0] == '"' && valor[len(valor)-1] == '"' {
			s, err := strconv.Unquote(valor)
			if err != nil {
				return nil, fmt.Errorf("línea %d: cadena mal formada: %w", linea, err)
			}
			valor = s
		}
		if seccion != "" {
			clave = seccion + "." + clave
		}
		valores[clave] = valor
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return valores, nil
}
