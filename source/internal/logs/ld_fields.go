package logs

// Fields: a message's body read as name=value pairs, when the search asks for them (field=value in a search, the Logs page's
// field list, the top values of a field).  Nothing is stored: the fields are read from the text each time, so every message
// already kept has them, and a new format here applies to old messages too.
//
// What is read, in this order (the first that fits; the structured data and the Cisco mnemonic are read as well):
//   - CEF (`CEF:0|vendor|product|version|signature|name|severity|ext`): the header as cef_*, the extension's key=value pairs
//   - PAN-OS CSV (TRAFFIC and THREAT logs: the fields by position; other types: serial, type and subtype)
//   - a JSON object (the message, or what follows the first "{"), flattened with dots (a.b.c), arrays left out
//   - key=value pairs, key="quoted value" or key='quoted' (FortiGate, Linux audit, Juniper, most appliances)
//   - RFC 5424 structured data: each parameter, by its name
//   - a Cisco mnemonic (%ASA-6-302013:, %LINK-3-UPDOWN:): cisco_facility, cisco_severity, cisco_mnemonic, and for the ASA's
//     deny messages (106001, 106006, 106007, 106015, 106023, 106100) the addresses, ports and interfaces.
//
// Names are lower case.  Common names get an alias too, so one search finds them across vendors: src_ip, dst_ip, src_port,
// dst_port, action, proto, user (src and dst become src_ip and dst_ip only when they hold an address, as iptables and CEF write them).  ldFieldsVersion changes whenever what is read changes (the word index records which one it
// was made with).

const ldFieldsVersion = 1
