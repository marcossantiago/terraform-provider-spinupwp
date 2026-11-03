data "spinupwp_server" "web" {
  name = "my-server"
}

output "web_ip" {
  value = data.spinupwp_server.web.ip_address
}
