package config

import "testing"

// EL EJEMPLO DEL REPOSITORIO ES LO QUE UN OPERADOR COPIA AL NODO.
//
// El lector de TOML de este paquete es deliberadamente mínimo (ver toml.go):
// admite un subconjunto y da error explícito ante todo lo demás. Eso hace que
// una sección nueva mal escrita en el ejemplo —una tabla anidada, un array, una
// cadena sin cerrar— no se descubra al escribirla, sino al arrancar el
// servicio, en el nodo, con el NAS parado.
//
// Esta prueba cierra ese hueco leyendo el archivo REAL del repositorio, no una
// copia. Si el ejemplo y el lector divergen, falla aquí.
func TestElEjemploDelRepositorioSeLeeEntero(t *testing.T) {
	c, err := Cargar("../../despliegue/nasd.toml.ejemplo")
	if err != nil {
		t.Fatalf("el ejemplo del repositorio no se puede leer: %v", err)
	}

	// Se comprueban las claves cuyo valor por omisión GOBIERNA EN PRODUCCIÓN:
	// 05_instalar_servicio.sh escribe el TOML del nodo solo la primera vez, así
	// que a un nodo que ya existe estas claves llegan por el binario y no por el
	// archivo. Que el ejemplo diga lo mismo que porDefecto() es lo que evita que
	// el documento y el producto se contradigan.
	//
	// Las de avisos, por ser las últimas en llegar (ADR-0073). Las de sesión,
	// porque el TOML del nodo NO declara sección [sesion] —comprobado el
	// 2026-09-03— y ADR-0082 acaba de cambiar el plazo de inactividad en tres
	// sitios a la vez: el binario, el ejemplo y el script de instalación. Si
	// alguno se queda atrás, el ejemplo prometería un plazo que el nodo no tiene.
	porOmision := porDefecto()
	for _, caso := range []struct {
		clave         string
		leido, espera any
	}{
		{"avisos.modo", c.ModoAvisos, porOmision.ModoAvisos},
		{"avisos.resumen_horas", c.PeriodoResumen, porOmision.PeriodoResumen},
		{"avisos.latido_s", c.IntervaloLatido, porOmision.IntervaloLatido},
		{"sesion.duracion_horas", c.DuracionSesion, porOmision.DuracionSesion},
		{"sesion.inactividad_s", c.InactividadSesion, porOmision.InactividadSesion},
	} {
		if caso.leido != caso.espera {
			t.Errorf("%s = %v; el ejemplo debe coincidir con el valor por omisión (%v)",
				caso.clave, caso.leido, caso.espera)
		}
	}
}
