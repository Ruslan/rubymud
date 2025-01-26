Sequel.migration do
  change do
    create_table :histories do
      primary_key :id
      String :line, null: false
      String :source
      DateTime :created_at, default: Sequel::CURRENT_TIMESTAMP
    end
  end
end
