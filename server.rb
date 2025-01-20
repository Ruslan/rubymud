require 'bundler'
Bundler.require

require './server/mud_client.rb'
require './server/configurable.rb'
require './server/game_engine.rb'
require './server/web_server.rb'

config_file = File.exist?('./config.rb') ? './config.rb' : './config.example.rb'
require config_file

web_server = WebServer.new

get '/' do
  File.read('public/index.html')
end

get '/ws' do
  if Faye::WebSocket.websocket?(request.env)
    ws = Faye::WebSocket.new(request.env)

    ws.on(:open) do |event|
      web_server.websockets << ws
      web_server.setup(ws)
    end

    ws.on(:message) do |msg|
      puts "WS input: #{msg.data}"
      begin
        web_server.incoming(JSON.parse(msg.data))
      rescue StandardError => e
        puts "Error: #{e.inspect}"
        puts e.backtrace
      end
    end

    ws.on(:close) do |event|
      web_server.websockets.delete(ws)
    end

    ws.rack_response
  else
    erb :index
  end
end

Thread.new do
  # I use mud-mux to continue sesssion after restart https://github.com/Ruslan/mud-mux
  mud = MudClient.new('localhost', 8888)
  web_server.mud = mud
  mud.income_handler do |text|
    web_server.message_from_server(text)
  end
  mud.connect
end
