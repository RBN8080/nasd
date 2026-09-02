# ---------------------------------------------------------------------------
#  testigo.ps1  -  El latido del CLIENTE hacia el testigo externo.
#
#  Contrato: 30_CLIENTE_RESPALDO.md seccion 13.3. Referencia: ADR-0074 (el del nodo).
#
#  MISMO SISTEMA, OTRO RELOJ. Y ahi esta todo:
#
#                    NODO                        EQUIPO
#    normal          encendido siempre           APAGADO MEDIA JORNADA
#    su silencio     significa que murio         no significa nada, es de noche
#    medida          latido 5 min, Grace 90 min  "hubo una corrida buena en los
#                                                 ultimos N dias"
#
#  Ponerle al cliente el latido de ADR-0074 haria sonar la alarma CADA
#  MADRUGADA, que es la regla de ruido de seccion 11 rota a diario. Y una alerta que
#  llega todos los dias deja de leerse justo antes del dia que importaba.
#
#  UN SEGUNDO CHECK en la cuenta de Healthchecks.io que YA EXISTE, no un
#  sistema paralelo: dos sistemas de vigilancia que no se conocen es peor que
#  uno.
#
#  EL "ESTOY BIEN" SOLO SE EMITE SI LA CORRIDA TERMINO **Y** LA VERIFICACION
#  PASO. Si se emitiera al acabar de copiar, un cliente que copia basura o que
#  no verifica SE VERIA SANO - que es exactamente el engano contra el que se
#  construyo ADR-0074. El aviso no dice "corri": dice "corri y comprobe".
#
#  Period y Grace se miden en DIAS, no en minutos, con holgura para que un fin
#  de semana con el equipo apagado no despierte a nadie. Los dos numeros se
#  calibran con datos reales en la Fase 2, igual que el umbral del freno.
#
#  LA URL DEL CHECK ES UN SECRETO, y ADR-0074 dice que es el que MAS DANO HACE
#  -mas que el token-. No vive en este repositorio (seccion 15 criterio 13).
#
#  Fase 0: esqueleto. Aqui no hay implementacion todavia.
# ---------------------------------------------------------------------------

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Pendiente de Fase 3.
