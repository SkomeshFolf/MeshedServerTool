set PATH=%PATH%;C:\Users\jaxez\AppData\Local\Packages\PythonSoftwareFoundation.Python.3.11_qbz5n2kfra8p0\LocalCache\local-packages\Python311\Scripts\
gunicorn -w 1 -b 0.0.0.0:8000 MeshWebServer:app
pause