//go:build !linux

package main

import (
	"net"
	"net/netip"
)

// En el host de desarrollo no hay /proc, así que se usa la vía normal.
//
// ESTO SOLO EXISTE PARA PODER COMPILAR Y PROBAR EN EL HOST (D-06), igual que
// sus gemelos de fsposix, autenticacion, metricas y seguridad. El objetivo de
// despliegue es linux/arm64, donde siempre corre la implementación real —que
// además es la única que sortea el RestrictAddressFamilies de la unidad.
func direccionesDelNodo() []netip.Addr {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []netip.Addr
	for _, i := range interfaces {
		dirs, err := i.Addrs()
		if err != nil {
			continue
		}
		for _, d := range dirs {
			n, ok := d.(*net.IPNet)
			if !ok {
				continue
			}
			if ip, ok := netip.AddrFromSlice(n.IP); ok {
				out = append(out, ip.Unmap())
			}
		}
	}
	return out
}
