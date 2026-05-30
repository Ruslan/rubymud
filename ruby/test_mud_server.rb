#!/usr/bin/env ruby
# frozen_string_literal: true

require 'socket'

host = ENV.fetch('HOST', '127.0.0.1')
port = Integer(ENV.fetch('PORT', '4000'))

server = TCPServer.new(host, port)
puts "Test MUD server listening on #{host}:#{port}"
puts 'Connect RubyMUD to this host/port and send BELL to emit ASCII BEL.'

def send_line(socket, text = '')
  socket.write("#{text}\r\n")
end

def initialize_session(socket)
  send_line(socket, 'RubyMUD BEL test server')
  send_line(socket, 'Commands: BELL, HELP, QUIT')
  send_line(socket, 'Send BELL to receive an actual ASCII BEL before the system message.')
  send_line(socket, '>')
end

def send_bell_sequence(socket)
  socket.write("\a[*** СИСТЕМА: Перезагрузка через 30 минут. ***]\r\n")
  send_line(socket, '>')
end

loop do
  client = server.accept

  Thread.new(client) do |socket|
    peer = socket.peeraddr(false)
    puts "Client connected: #{peer[2]}:#{peer[1]}"

    begin
      initialize_session(socket)

      while (line = socket.gets)
        command = line.strip

        case command.upcase
        when 'BELL'
          send_bell_sequence(socket)
        when 'HELP'
          send_line(socket, 'Commands: BELL emits ASCII BEL, QUIT closes the session.')
          send_line(socket, '>')
        when 'QUIT', 'EXIT'
          send_line(socket, 'Bye.')
          break
        when ''
          send_line(socket, '>')
        else
          send_line(socket, "You said: #{command}")
          send_line(socket, '>')
        end
      end
    rescue IOError, SystemCallError => e
      warn "Client error: #{e.class}: #{e.message}"
    ensure
      socket.close unless socket.closed?
      puts "Client disconnected: #{peer[2]}:#{peer[1]}"
    end
  end
end
