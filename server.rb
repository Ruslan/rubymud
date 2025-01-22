require 'bundler'
Bundler.require

loader = Zeitwerk::Loader.new
loader.push_dir(File.join(__dir__, 'server'))
loader.setup

config_file = File.exist?('./config.rb') ? './config.rb' : './config.example.rb'
require config_file

web_server = WebServer.new

class MyApp < Sinatra::Base
  configure do
    set :web_server, nil
    set :host_authorization, { permitted_hosts: [] }
  end

  def web_server
    settings.web_server
  end

  get '/' do
    File.read('public/index.html')
  end

  get '/ws' do
    if Faye::WebSocket.websocket?(request.env)
      ws = Faye::WebSocket.new(request.env)

      ws.on(:open) do |event|
        web_server.websockets << ws
        web_server.reload
        web_server.setup(ws)
        web_server.restore_history
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
      'Socket not supported'
    end
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

MyApp.settings.web_server = web_server
MyApp.run!
