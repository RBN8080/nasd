//go:build linux

package main

import (
	"bufio"
	"encoding/hex"
	"net/netip"
	"os"
	"strings"
)

// direccionesDelNodo lee las IPv6 del nodo de /proc/net/if_inet6.
//
// # POR QUÉ NO net.Interfaces(), QUE ES LO OBVIO
//
// Porque no funciona aquí, y se descubrió DESPLEGÁNDOLO: la primera versión
// usaba net.Interfaces() y el nodo avisó al arrancar de que no había
// aprendido ninguna red. La causa es que net.Interfaces() abre un socket
// AF_NETLINK y la unidad de systemd lleva:
//
//	RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
//
// AF_NETLINK no está, así que la llamada falla dentro del servicio aunque
// funcione perfectamente por SSH. Medido: `ip -br addr` mostraba las dos
// IPv6 globales del nodo mientras nasd no veía ninguna.
//
// LA SALIDA NO ES AÑADIR AF_NETLINK A LA UNIDAD. Eso rebajaría el
// endurecimiento del servicio —hoy en 3.6 según systemd-analyze— a cambio de
// una función de observabilidad, que es justo la desproporción que P8
// prohíbe. /proc/net/if_inet6 es un archivo de texto normal y da exactamente
// el mismo dato.
//
// Formato (proc(5)): dirección en 32 dígitos hexadecimales sin dos puntos,
// índice de interfaz, longitud de prefijo, ÁMBITO, banderas y nombre. El
// ámbito 00 es «global», que es el único que interesa: fe80::/10 tiene ámbito
// 20 y nadie habla con el NAS por ahí.
func direccionesDelNodo() []netip.Addr {
	f, err := os.Open("/proc/net/if_inet6")
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []netip.Addr
	s := bufio.NewScanner(f)
	for s.Scan() {
		campos := strings.Fields(s.Text())
		if len(campos) < 4 {
			continue
		}
		if campos[3] != "00" { // solo ámbito global
			continue
		}
		crudo, err := hex.DecodeString(campos[0])
		if err != nil || len(crudo) != 16 {
			continue
		}
		if ip, ok := netip.AddrFromSlice(crudo); ok {
			out = append(out, ip)
		}
	}
	return out
}
