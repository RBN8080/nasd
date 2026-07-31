package almacen

import "errors"

// Errores tipados del dominio.
//
// Charter §6.1: errores tipados y específicos; prohibido descartar un error.
// El adaptador HTTP los traduce a códigos de estado; el núcleo no sabe que
// existen los códigos de estado.
var (
	// ErrRutaInvalida — la entrada del usuario no puede convertirse en una
	// ruta contenida en la raíz de datos. CWE-22.
	ErrRutaInvalida = errors.New("ruta inválida")

	// ErrNoExiste — la entrada solicitada no está en el almacén.
	ErrNoExiste = errors.New("no existe")

	// ErrYaExiste — el destino ya está ocupado. RF-23: no se sobrescribe
	// sin decisión explícita del usuario.
	ErrYaExiste = errors.New("ya existe")

	// ErrNoEsDirectorio — se pidió listar algo que no es un directorio.
	ErrNoEsDirectorio = errors.New("no es un directorio")

	// ErrEnlaceExterno — la entrada es un enlace simbólico que apunta fuera
	// del volumen. Lo detiene la capa 2 de 04_SEGURIDAD.md §2 (os.Root), que
	// es la única que puede verlo: una comprobación de cadenas no distingue
	// un enlace de un archivo normal.
	//
	// Se distingue de ErrNoExiste PARA EL REGISTRO, no para el cliente: al
	// cliente se le responde 404, porque confirmar que ahí hay un enlace
	// hacia fuera ya es información que no le corresponde.
	ErrEnlaceExterno = errors.New("enlace simbólico fuera del volumen")

	// ErrEsDirectorio — se pidió como archivo algo que es un directorio.
	ErrEsDirectorio = errors.New("es un directorio")

	// ErrNoVacio — se intentó borrar un directorio con contenido usando la
	// operación no recursiva. Destruir un árbol exige pedirlo por su nombre.
	ErrNoVacio = errors.New("el directorio no está vacío")

	// ErrDentroDeSiMismo — mover un directorio dentro de su propio subárbol.
	// El sistema lo rechazaría, pero conviene un error del dominio claro.
	ErrDentroDeSiMismo = errors.New("no se puede mover un directorio dentro de sí mismo")

	// ErrOcupado — otro escritor tiene el destino tomado. ADR-0028, capa 3.
	ErrOcupado = errors.New("ocupado por otro escritor")

	// ErrDesplazamiento — el Upload-Offset declarado por el cliente no
	// coincide con el tamaño real del parcial. ADR-0027.
	ErrDesplazamiento = errors.New("desplazamiento incoherente")
)
