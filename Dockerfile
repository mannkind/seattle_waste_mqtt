FROM mcr.microsoft.com/dotnet/sdk:5.0 as build
WORKDIR /src
COPY . .
RUN if [ ! -d output ]; then dotnet build -o output -c Release SeattleWaste; fi

FROM mcr.microsoft.com/dotnet/runtime:5.0 AS runtime
COPY --from=build /src/output app
ENTRYPOINT ["dotnet", "./app/SeattleWaste.dll"]
